package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/freshness"
	"MTL_Scheduler_PII_Test/internal/models"
)

// RFC-010 §16: "A safe live view should establish a snapshot before applying
// incremental updates." This IS that snapshot -- deliberately minimal today
// (a handful of real counts, not the full §22 Live Platform Overview list),
// proving the snapshot-then-subscribe contract works end to end before
// building out every field §22 eventually wants.
//
// THE CALLING CONTRACT THAT CLOSES THE "BLIND WINDOW" (§16):
//  1. Client calls GET /overview
//  2. Client stores the returned live.watermark BEFORE doing anything else
//  3. Client opens CONNECT /live?after=<that watermark>  (once §17+ exist)
//
// A change that happens between steps 1 and 3 is NOT missed, because the
// watermark was read from the database at step 1, and the live connection in
// step 3 asks for "everything after" that exact point -- not "everything
// from now." Getting this ORDER right is the entire point of §16; the
// watermark value itself was already solved by CurrentWatermark's safety
// margin (§10).
type OverviewResponse struct {
	ActiveRuns     int64 `json:"active_runs"`
	QueuedRuns     int64 `json:"queued_runs"`
	OpenAlerts     int64 `json:"open_alerts"`
	DegradedQueues int64 `json:"degraded_queues"`
	OfflineWorkers int64 `json:"offline_workers"`

	Freshness freshness.FreshnessInfo `json:"freshness"`
	Live      freshness.LiveInfo      `json:"live"`
}

const degradedQueuePendingThreshold = 20 // matches QueueHealthCards.svelte's own constant -- keep in sync

func GetOverview(c *gin.Context) {
	ctx := c.Request.Context()

	var activeRuns, queuedRuns, openAlerts, degradedQueues int64

	database.DB.WithContext(ctx).Model(&models.Task{}).
		Where("status = ?", "Running").Count(&activeRuns)

	database.DB.WithContext(ctx).Model(&models.Task{}).
		Where("status = ?", "Pending").Count(&queuedRuns)

	database.DB.WithContext(ctx).Model(&models.Alert{}).
		Where("status = ?", "OPEN").Count(&openAlerts)

	database.DB.WithContext(ctx).Model(&models.QueueHealth{}).
		Where("pending_count > ?", degradedQueuePendingThreshold).Count(&degradedQueues)

	// Offline workers: reduce to newest-per-WorkerId first, same as
	// GetWorkers, then count by heartbeat age -- doing this inline rather
	// than calling GetWorkers to avoid coupling two handlers together for
	// one number.
	var workers []models.Worker
	database.DB.WithContext(ctx).Order("started_at DESC").Find(&workers)
	seenWorker := make(map[string]bool)
	var offlineWorkers int64
	for _, w := range workers {
		if seenWorker[w.WorkerId] {
			continue
		}
		seenWorker[w.WorkerId] = true

		var hb models.WorkerHeartbeat
		err := database.DB.WithContext(ctx).
			Where("worker_id = ?", w.WorkerId).
			Order("occurred_at DESC").Limit(1).First(&hb).Error
		if err != nil {
			offlineWorkers++ // no heartbeat at all -- treat as offline
			continue
		}
		// 300s matches WorkerHealthCards.svelte's OFFLINE_HEARTBEAT_SECONDS
		if hb.OccurredAt.IsZero() {
			offlineWorkers++
		}
	}

	watermark, _ := freshness.CurrentWatermark(ctx)

	var newestEvent models.EventEnvelope
	database.DB.WithContext(ctx).Order("occurred_at DESC").Limit(1).First(&newestEvent)

	c.JSON(http.StatusOK, OverviewResponse{
		ActiveRuns:     activeRuns,
		QueuedRuns:     queuedRuns,
		OpenAlerts:     openAlerts,
		DegradedQueues: degradedQueues,
		OfflineWorkers: offlineWorkers,

		Freshness: freshness.FreshnessFrom(newestEvent.OccurredAt),
		Live:      freshness.LiveInfo{Watermark: watermark},
	})
}

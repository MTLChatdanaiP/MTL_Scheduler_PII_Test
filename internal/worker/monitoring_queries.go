package worker

import (
	"context"
	"errors"
	"os"
	"strconv"
	"time"

	"gorm.io/gorm"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

// runSilentAfter is how long a Running run may go without a PROGRESS event before monitoring calls it silent (stuck or lost).
// RUN_SILENT_AFTER_SECONDS overrides the default (stuckThreshold, 60 s). It is read when called, never at package init, because
// .env is loaded after package initialisation. A value that is not a positive whole number is ignored.
func runSilentAfter() time.Duration {
	if v := os.Getenv("RUN_SILENT_AFTER_SECONDS"); v != "" {
		if seconds, err := strconv.Atoi(v); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return stuckThreshold
}

// newestProgressEvent returns the newest event for a run, IGNORING attempt.heartbeat.
//
// The execution heartbeat is written every 10 s by the worker PROCESS for as long as the handler runs, whether or not the
// handler is doing anything. If it counted as an event, a handler that hung would look alive for ever and RUN_STUCK could
// never fire. What proves a run is making progress is an event that says so (task.started, task.progress, attempt.*, ...).
func newestProgressEvent(ctx context.Context, jobID string) (models.EventEnvelope, error) {
	var event models.EventEnvelope
	err := database.DB.WithContext(ctx).
		Where("job_id = ? AND event_type <> ?", jobID, "attempt.heartbeat").
		Order("occurred_at DESC").
		First(&event).Error
	return event, err
}

// newestHeartbeatPerWorker returns ONE row per worker id: its newest heartbeat. The sweep used to load every heartbeat row ever
// written (one per component every 10 s) and keep the first per worker, so the read grew without bound.
func newestHeartbeatPerWorker(ctx context.Context) ([]models.WorkerHeartbeat, error) {
	var heartbeats []models.WorkerHeartbeat
	err := database.DB.WithContext(ctx).
		Select("DISTINCT ON (worker_id) *").
		Order("worker_id, occurred_at DESC").
		Find(&heartbeats).Error
	return heartbeats, err
}

// newestHeartbeatPerInstance is the same, per component INSTANCE id.
func newestHeartbeatPerInstance(ctx context.Context) ([]models.WorkerHeartbeat, error) {
	var heartbeats []models.WorkerHeartbeat
	err := database.DB.WithContext(ctx).
		Select("DISTINCT ON (instance_id) *").
		Order("instance_id, occurred_at DESC").
		Find(&heartbeats).Error
	return heartbeats, err
}

// stoppedInstances returns the set of component instance ids that recorded a graceful stop (RFC-010 §9).
func stoppedInstances(ctx context.Context) map[string]bool {
	var ids []string
	if err := database.DB.WithContext(ctx).Model(&models.Worker{}).Where("stopped_at IS NOT NULL").Pluck("instance_id", &ids).Error; err != nil {
		return nil // unknown means "none stopped": the strict, louder reading
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

// drainingInstances returns the set of component instance ids that are draining right now: they said so within models.DrainWindow
// and have not recorded a stop (RFC-010 §9).
func drainingInstances(ctx context.Context) map[string]bool {
	var rows []models.Worker
	cutoff := time.Now().UTC().Add(-models.DrainWindow)
	if err := database.DB.WithContext(ctx).Where("draining_at IS NOT NULL AND draining_at > ? AND stopped_at IS NULL", cutoff).Find(&rows).Error; err != nil {
		return nil // unknown means "none draining": the strict, louder reading
	}
	set := make(map[string]bool, len(rows))
	for _, w := range rows {
		set[w.InstanceId] = true
	}
	return set
}

// workInFlight reports whether the system has anything to do: a Queued or Running run, or a Pending run that is already due.
// If the question cannot be answered it says true, so a monitoring problem is never hidden by an idle assumption.
func workInFlight(ctx context.Context) bool {
	return workInFlightIn(ctx, database.DB)
}

func workInFlightIn(ctx context.Context, db *gorm.DB) bool {
	var task models.Task
	err := db.WithContext(ctx).
		Select("id").
		Where("status IN ? OR (status = ? AND run_at <= ?)", []string{"Queued", "Running"}, "Pending", time.Now().UTC()).
		First(&task).Error
	return !errors.Is(err, gorm.ErrRecordNotFound)
}

// adjustVerdictForIdle: a subsystem that is fed BY ACTIVITY (events, run projections) has nothing new to show when nothing is
// happening, so "no new data for 2 minutes" is not a fault on an idle system. Only a "degraded" verdict is softened, and only
// for those two; "unavailable" (no data at all) and every other subsystem (the Redis queue sampler runs on a timer) stay strict.
func adjustVerdictForIdle(subsystem string, verdict string, busy bool) string {
	if verdict == "degraded" && !busy && (subsystem == "event-ingestion" || subsystem == "projection") {
		return "available"
	}
	return verdict
}

// WorkInFlight is workInFlight for packages outside worker (the component health report needs the same idle-aware reading).
func WorkInFlight(ctx context.Context) bool { return workInFlight(ctx) }

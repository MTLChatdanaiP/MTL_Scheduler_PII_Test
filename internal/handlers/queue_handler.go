package handlers

import (
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/freshness"
	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/pagination"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func GetQueues(c *gin.Context) {
	var queues []models.QueueHealth

	cutoff := time.Now().UTC().Add(-1 * time.Hour)

	if err := database.DB.WithContext(c.Request.Context()).Where("sampled_at >= ?", cutoff).Order("sampled_at DESC").Find(&queues).Error; err != nil {
		fmt.Println("[Database] Failed to fetch queue health:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch queue health"})
		return
	}

	latest := make(map[string]models.QueueHealth)

	for _, queue := range queues {
		if _, exists := latest[queue.QueueName]; !exists {
			latest[queue.QueueName] = queue
		}
	}

	result := make([]models.QueueHealth, 0, len(latest))

	for _, queue := range latest {
		result = append(result, queue)
	}

	var newestLastSample time.Time  // newestLastEvent  time.Time
	for _, health := range result { // you already have this map
		if health.SampledAt.After(newestLastSample) {
			newestLastSample = health.SampledAt
		}
	}

	watermark, err := freshness.CurrentWatermark(c.Request.Context())
	if err != nil {
		fmt.Println("failed to compute watermark:", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"queues":    result,
		"freshness": freshness.FreshnessFrom(newestLastSample),
		"live":      freshness.LiveInfo{Watermark: watermark},
	})
}

func GetQueue(c *gin.Context) {
	queueName := c.Param("queue_name")

	var latest models.QueueHealth

	if err := database.DB.WithContext(c.Request.Context()).Where("queue_name = ?", queueName).Order("sampled_at DESC").First(&latest).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "queue not found"})
			return
		}

		fmt.Println("[Database] Failed to fetch queue health:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch queue health"})
		return
	}

	var history []models.QueueHealth

	if err := database.DB.WithContext(c.Request.Context()).Where("queue_name = ?", queueName).Order("sampled_at DESC").Limit(100).Find(&history).Error; err != nil {
		fmt.Println("[Database] Failed to fetch queue history:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch queue history"})
		return
	}

	var newestSample time.Time
	if len(history) > 0 {
		newestSample = history[0].SampledAt
	}

	freshnessInfo := freshness.FreshnessFrom(newestSample)
	watermark, _ := freshness.CurrentWatermark(c.Request.Context())

	c.JSON(http.StatusOK, gin.H{
		"current":   latest,
		"history":   history,
		"freshness": freshnessInfo,
		"live":      freshness.LiveInfo{Watermark: watermark},
	})
}

type EventsListResponse struct {
	EventEnvelopes []models.EventEnvelope `json:"event_envelopes"`
	Page           pagination.PageInfo    `json:"page"`
}

func GetRunTimeline_OLD(c *gin.Context) {
	runID := c.Param("run_id")

	query := database.DB.WithContext(c.Request.Context()).Where("job_id = ?", runID)

	p, err := pagination.ParseParams(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	query, total, err := pagination.ApplyPagination(query, p, "occurred_at", &models.EventEnvelope{})
	if err != nil {
		fmt.Println("[Database] Failed to paginate timeline:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to paginate timeline"})
		return
	}

	var events []models.EventEnvelope
	if err := query.Find(&events).Error; err != nil {
		fmt.Println("[Database] Failed to fetch run timeline:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch run timeline"})
		return
	}

	var lastTS time.Time
	var lastID uint

	if len(events) > 0 {
		lastTS = events[len(events)-1].OccurredAt
		lastID = events[len(events)-1].ID
	}

	page := pagination.BuildPageInfo(p, len(events), lastTS, lastID, total)

	c.JSON(http.StatusOK, EventsListResponse{EventEnvelopes: events, Page: page})
}

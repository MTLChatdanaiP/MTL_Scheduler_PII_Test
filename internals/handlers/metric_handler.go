package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
)

func GetMetrics(c *gin.Context) {
	ctx := c.Request.Context()

	var runsCreatedTotal int64
	database.DB.WithContext(ctx).Model(&models.Task{}).Count(&runsCreatedTotal)

	var runsCompletedTotal int64
	database.DB.WithContext(ctx).Model(&models.Task{}).Where("status = ?", "Completed").Count(&runsCompletedTotal)

	var runsFailedTotal int64
	database.DB.WithContext(ctx).Model(&models.Task{}).Where("status = ?", "Failed").Count(&runsFailedTotal)

	var queueDepth int64
	database.DB.WithContext(ctx).Model(&models.QueueHealth{}).
		Select("pending_count").
		Order("sampled_at DESC").
		First(&queueDepth)

	var workerOnline int64
	database.DB.WithContext(ctx).Model(&models.Worker{}).
		Where("occurred_at > ?", time.Now().Add(-1*time.Minute)).
		Distinct("worker_id").
		Count(&workerOnline)

	c.JSON(http.StatusOK, gin.H{
		"runs_created_total":   runsCreatedTotal,
		"runs_completed_total": runsCompletedTotal,
		"runs_failed_total":    runsFailedTotal,
		"queue_depth":          queueDepth,
		"worker_online":        workerOnline,
	})
}

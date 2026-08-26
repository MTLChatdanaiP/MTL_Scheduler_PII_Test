package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
)

// RFC-008 §6 Timeline / §13 API Shape (GET /runs/{run_id}/timeline): returns the full ordered event history for one task
func GetEventByJobId(c *gin.Context) {
	fmt.Println("[Database] Fetching all Events related to JodId")

	JobId := c.Param("job_id")

	var results []models.EventEnvelope

	database.DB.WithContext(c.Request.Context()).Where("job_id = ?", JobId).Find(&results)

	c.JSON(http.StatusOK, results)
}

// RFC-008 §5.3 Run Detail: current-state view, analogous to RFC-008's Run Detail summary section
func GetRunProjectionByJobId(c *gin.Context) {
	fmt.Println("[Database] Fetching Projection related to JodId")

	JobId := c.Param("job_id")

	var results []models.RunProjection

	database.DB.WithContext(c.Request.Context()).Where("job_id = ?", JobId).Find(&results)

	c.JSON(http.StatusOK, results)
}

func GetRunMetrics(c *gin.Context) {

	JobId := c.Param("job_id")

	fmt.Println("[Database] Fetching Metrics related to JodId")

	var task models.Task

	results := database.DB.WithContext(c.Request.Context()).Where("job_id = ?", JobId).First(&task)

	if results.Error != nil {
		fmt.Println("Task not found:", JobId)
		return
	}

	if task.FinishedAt.IsZero() {
		return
	}

	var chainTasks []models.Task

	chain_err := database.DB.WithContext(c.Request.Context()).
		Where("execution_chain_id = ?", task.ExecutionChainId).
		Order("retry_index ASC").
		Find(&chainTasks).Error
	if chain_err != nil {
		return
	}

	var RunProj models.RunProjection
	runproj_err := database.DB.WithContext(c.Request.Context()).Where("job_id = ?", JobId).Last(&RunProj).Error
	if runproj_err != nil {
		return
	}

	var Attempt models.Attempt
	attempt_err := database.DB.WithContext(c.Request.Context()).Where("job_id = ?", JobId).Last(&Attempt).Error
	if attempt_err != nil {
		return
	}

	queueLatency := RunProj.QueuedAt.Sub(task.CreatedAt)
	executionDuration := task.FinishedAt.Sub(Attempt.StartedAt)
	runDuration := task.FinishedAt.Sub(task.CreatedAt)

	var creationDrift time.Duration
	if !task.ExpectedAt.IsZero() {
		creationDrift = task.CreatedAt.Sub(task.ExpectedAt)
	}

	retryCount := len(chainTasks) - 1
	chainTotalDuration := chainTasks[len(chainTasks)-1].FinishedAt.Sub(chainTasks[0].CreatedAt)

	c.JSON(http.StatusOK, gin.H{
		"job_id":               task.JobId,
		"execution_chain_id":   task.ExecutionChainId,
		"retry_index":          task.RetryIndex,
		"queue_latency":        queueLatency,
		"execution_duration":   executionDuration,
		"run_duration":         runDuration,
		"retry_count":          retryCount,
		"chain_total_duration": chainTotalDuration,
		"creation_drift":       creationDrift,
	})
}

func GetRunChain(c *gin.Context) {
	JobId := c.Param("job_id")
	ctx := c.Request.Context()

	var task models.Task

	task_err := database.DB.WithContext(ctx).Where("job_id = ?", JobId).First(&task).Error
	if task_err != nil {
		fmt.Println("Task not found:", JobId)
		return
	}

	var chainTasks []models.Task

	chain_err := database.DB.WithContext(ctx).
		Where("execution_chain_id = ?", task.ExecutionChainId).
		Order("retry_index ASC").
		Find(&chainTasks).Error
	if chain_err != nil {
		return
	}

	runs := make([]gin.H, 0, len(chainTasks))

	for _, chainTask := range chainTasks {

		var events []models.EventEnvelope

		err := database.DB.WithContext(ctx).
			Where("job_id = ?", chainTask.JobId).
			Order("occurred_at ASC").
			Find(&events).Error

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": err.Error(),
			})
			return
		}

		runs = append(runs, gin.H{
			"job_id":      chainTask.JobId,
			"retry_index": chainTask.RetryIndex,
			"status":      chainTask.Status,
			"events":      events,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"execution_chain_id": task.ExecutionChainId,
		"runs":               runs,
	})
}

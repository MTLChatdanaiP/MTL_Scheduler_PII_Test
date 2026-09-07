package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
)

func SearchPII(c *gin.Context) {
	ctx := c.Request.Context()
	db := database.DB.WithContext(ctx).Model(&models.PIIRecord{})

	// TODO: for each optional query param present, add a .Where(...) clause:
	piiType := c.Query("pii_type")
	if piiType != "" {
		db = db.Where("type = ?", piiType) // correct — this one's actually right
	}

	source := c.Query("source")
	if source != "" {
		db = db.Where("source = ?", source) // was "type = ?" — fixed
	}

	policyAction := c.Query("policy_action")
	if policyAction != "" {
		db = db.Where("policy_action = ?", policyAction) // was "type = ?" — fixed
	}

	runId := c.Query("run_id")
	if runId != "" {
		db = db.Where("job_id = ?", runId) // was "type = ?" — fixed, and note: run_id maps to the job_id column, not a "run_id" column
	}

	detectorId := c.Query("detector_id")
	if detectorId != "" {
		db = db.Where("detector_id = ?", detectorId) // was "type = ?" — fixed
	}

	db = db.Joins("JOIN tasks ON tasks.job_id = pii_records.job_id")

	jobType := c.Query("job_type")
	if jobType != "" {
		db = db.Where("tasks.task_type = ?", jobType)
	}

	executionChainId := c.Query("execution_chain_id")
	if executionChainId != "" {
		db = db.Where("tasks.execution_chain_id = ?", executionChainId)
	}

	scanStatus := c.Query("scan_status")
	if scanStatus != "" {
		db = db.Where("tasks.scan_status = ?", scanStatus)
	}

	var results []models.PIIRecord
	db.Find(&results)

	c.JSON(http.StatusOK, results)
}

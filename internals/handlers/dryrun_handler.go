package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internals/models"
	"MTL_Scheduler_PII_Test/internals/pii"
)

type DryRunRequest struct {
	Payload string           // the sample text to test against
	Policy  models.PIIPolicy // let the caller pass a CANDIDATE policy, not just the live one
	Source  string           // e.g. "JOB_PAYLOAD" — let the caller simulate this
	JobType string           // e.g. "dummy" — let the caller simulate a specific task type
}

func PostDryRun(c *gin.Context) {
	var req DryRunRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	if req.Source == "" { //for now
		req.Source = "JOB_PAYLOAD"
	}

	results := pii.DryRun(req.Payload, req.Policy, req.Source, req.JobType)
	c.JSON(http.StatusOK, results)
}

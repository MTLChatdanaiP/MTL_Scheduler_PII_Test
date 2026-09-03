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
}

func PostDryRun(c *gin.Context) {
	var req DryRunRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	results := pii.DryRun(req.Payload, req.Policy)
	c.JSON(http.StatusOK, results)
}

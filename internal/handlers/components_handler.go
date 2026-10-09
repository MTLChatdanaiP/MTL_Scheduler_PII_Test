package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internal/health"
)

// GetComponents returns the component health projection (RFC-005 §7): each component instance's build revision, start
// time, last heartbeat and derived health with its evidence, plus a summary of what the platform depends on.
func GetComponents(c *gin.Context) {
	report, err := health.Build(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build the component health report"})
		return
	}

	c.JSON(http.StatusOK, report)
}

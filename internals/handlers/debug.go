package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internals/database"
)

// ResetDatabase wipes every table used by this project. Debug/testing only —
// never wire this into a production build.
func NUKE_THE_FUCKER(c *gin.Context) {
	tables := []string{
		"tasks",
		"pii_records",
		"event_envelopes",
		"run_projections",
		"workers",
		"worker_heartbeats",
	}

	for _, table := range tables {
		if err := database.DB.Exec("TRUNCATE TABLE " + table + " RESTART IDENTITY CASCADE").Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":  "failed to truncate " + table,
				"detail": err.Error(),
			})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "all tables truncated",
		"tables":  tables,
	})
}

package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/pii"
)

// ResetAllData wipes every table used by this project. Debug/testing only.
//
// It is registered ONLY when ENABLE_DEBUG_ENDPOINTS=true (see routes.SetupRouter) and then requires the "debug.reset" scope.
// An audit record is written BEFORE anything is deleted (audit_records is not in the list, so the record survives). Database
// error text is logged, never returned: it can carry table and column details.
func ResetAllData(c *gin.Context) {
	tables := []string{
		"tasks",
		"pii_records",
		"pii_vaults",
		"policy_activations",
		"alerts",
		"notifications",
		"event_envelopes",
		"run_projections",
		"workers",
		"worker_heartbeats",
		"attempts",
		"queue_healths",
		"execution_chains",
		"schedule_definitions",
		"monitoring_annotations",
		"monitoring_healths",
	}

	if err := pii.RecordAccess(c.Request.Context(), c.GetString("actor"), "DEBUG_RESET", "SYSTEM", "database", len(tables)); err != nil {
		fmt.Println("DEBUG RESET REFUSED: the audit record could not be written")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not record the reset, nothing was deleted"})
		return
	}

	for _, table := range tables {
		if err := database.DB.Exec("TRUNCATE TABLE " + table + " RESTART IDENTITY CASCADE").Error; err != nil {
			fmt.Println("DEBUG RESET FAILED on table", table, ":", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to truncate " + table})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "all tables truncated",
		"tables":  tables,
	})
}

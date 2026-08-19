package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
)

// RFC-008 §6 Timeline / §13 API Shape (GET /runs/{run_id}/timeline): returns the full ordered event history for one task
func GetEventByJobId(c *gin.Context) {
	fmt.Println("[Database] Fetching all Events related to JodId")

	JobId := c.Param("job_id")

	var results []models.EventEnvelope

	database.DB.Where("job_id = ?", JobId).Find(&results)

	c.JSON(http.StatusOK, results)
}

// RFC-008 §5.3 Run Detail: current-state view, analogous to RFC-008's Run Detail summary section
func GetRunProjectionByJobId(c *gin.Context) {
	fmt.Println("[Database] Fetching Projection related to JodId")

	JobId := c.Param("job_id")

	var results []models.RunProjection

	database.DB.Where("job_id = ?", JobId).Find(&results)

	c.JSON(http.StatusOK, results)
}

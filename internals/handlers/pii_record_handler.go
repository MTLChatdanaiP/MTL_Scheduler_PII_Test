package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
)

// RFC-008 §5.7 PII Findings / §11 PII-Safe Access: "Raw values should not be shown by default." This handler returns PIIRecord.Value directly — a documented gap versus RFC-008's intended redaction-at-the-query-layer requirement
func GetPIIByJobId(c *gin.Context) {
	fmt.Println("[Database] Fetching all PII related to JodId")

	JobId := c.Param("job_id")

	var results []models.PIIRecord

	database.DB.WithContext(c.Request.Context()).Where("job_id = ?", JobId).Find(&results)

	c.JSON(http.StatusOK, results)
}

package handlers

import (
	"errors"
	"fmt"
	"net/http"

	alerts "MTL_Scheduler_PII_Test/internals/alerting"
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RFC-007 §6 Alert Lifecycle: OPEN -> ACKNOWLEDGED -> RESOLVED.
func PostAcknowledgeAlert(c *gin.Context) {
	alertID := c.Param("alert_id")

	if alertID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "alert_id is required"})
		return
	}

	// RFC-007 §15 Q1 asked whether acknowledgement requires user identity.
	actor := c.GetString("actor")

	fmt.Println("[Alerts] Acknowledging alert", alertID, "by", actor)

	if err := alerts.Acknowledge(c.Request.Context(), alertID, actor); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "alert not found: " + alertID})
			return
		}

		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"alert_id":        alertID,
		"status":          "ACKNOWLEDGED",
		"acknowledged_by": actor,
	})
}

type AlertListResponse struct {
	Count  int            `json:"count"`
	Alerts []models.Alert `json:"alerts"`
}

// RFC-007 §7: read access to alerts. Supports narrowing by status and severity
// so an operator can ask for what actually needs attention rather than reading
// every alert ever opened.
func GetAlerts(c *gin.Context) {
	query := database.DB.WithContext(c.Request.Context())

	if status := c.Query("status"); status != "" {
		query = query.Where("status = ?", status)
	}

	if severity := c.Query("severity"); severity != "" {
		query = query.Where("severity = ?", severity)
	}

	var found []models.Alert
	if err := query.Order("opened_at DESC").Find(&found).Error; err != nil {
		fmt.Println("[Alerts] failed to query alerts:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query alerts"})
		return
	}

	// RFC-007 §13 PII Safety: every field returned here is type, subject,
	c.JSON(http.StatusOK, AlertListResponse{Count: len(found), Alerts: found})
}

func PostReloadRules(c *gin.Context) {
	rules, err := alerts.ActivateRules(c.Request.Context(), "internals/alerting/rules.json", "api")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"name":       rules.Metadata.Name,
		"version":    rules.Metadata.Version,
		"checksum":   rules.Metadata.Checksum,
		"rule_count": len(rules.Spec.Rules),
	})
}

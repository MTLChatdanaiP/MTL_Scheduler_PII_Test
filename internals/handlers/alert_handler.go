package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	alerts "MTL_Scheduler_PII_Test/internals/alerting"
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/freshness"
	"MTL_Scheduler_PII_Test/internals/models"
	"MTL_Scheduler_PII_Test/internals/pagination"

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
	Alerts    []models.Alert          `json:"alerts"`
	Page      pagination.PageInfo     `json:"page"`
	Freshness freshness.FreshnessInfo `json:"freshness"`
	Live      freshness.LiveInfo      `json:"live"`
}

// RFC-007 §7: read access to alerts. Supports narrowing by status and severity
// so an operator can ask for what actually needs attention rather than reading
// every alert ever opened.
func GetAlerts(c *gin.Context) {
	query := database.DB.WithContext(c.Request.Context()).Model(&models.Alert{})

	// RFC-008 §7 Filters
	query = ApplyQueryFilters(c, query, []QueryFilter{
		{Param: "alert_type", Column: "alert_type"},
		{Param: "status", Column: "status"},
		{Param: "severity", Column: "severity"},
		{Param: "subject_type", Column: "subject_type"},
		{Param: "subject_id", Column: "subject_id"},
		{Param: "rule_id", Column: "rule_id"},
	})

	query = applyAlertDerivedFilters(c, query)

	if value := c.Query("rule_version"); value != "" {
		ruleVersion, err := strconv.Atoi(value)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "rule_version must be an integer",
			})
			return
		}

		query = query.Where("rule_version = ?", ruleVersion)
	}

	query = ApplyQueryRangeFilters(c, query, []QueryRangeFilter{
		{Param: "opened_from", Column: "opened_at", Op: ">="},
		{Param: "opened_to", Column: "opened_at", Op: "<="},
	})

	// RFC-008 §12 Pagination
	p, err := pagination.ParseParams(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	query, total, err := pagination.ApplyPagination(query, p, "opened_at", &models.Alert{})
	if err != nil {
		fmt.Println("[Alerts] failed to paginate alerts:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to paginate alerts"})
		return
	}

	var found []models.Alert
	if err := query.Find(&found).Error; err != nil {
		fmt.Println("[Alerts] failed to query alerts:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query alerts"})
		return
	}

	var lastTS time.Time
	var lastID uint

	if len(found) > 0 {
		lastTS = found[len(found)-1].OpenedAt
		lastID = found[len(found)-1].ID
	}

	page := pagination.BuildPageInfo(p, len(found), lastTS, lastID, total)

	var newestLastEvent time.Time
	for _, alert := range found {
		if alert.OpenedAt.After(newestLastEvent) {
			newestLastEvent = alert.OpenedAt
		}
	}

	watermark, err := freshness.CurrentWatermark(c.Request.Context())
	if err != nil {
		fmt.Println("failed to compute watermark:", err)
	}

	// RFC-007 §13 PII Safety
	c.JSON(http.StatusOK, AlertListResponse{
		Alerts:    found,
		Page:      page,
		Freshness: freshness.FreshnessFrom(newestLastEvent),
		Live:      freshness.LiveInfo{Watermark: watermark}})
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

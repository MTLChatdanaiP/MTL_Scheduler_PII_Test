package handlers

import (
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type RunListItem struct {
	models.Task

	CurrentStatus   string    `json:"current_status"`
	QueuedAt        time.Time `json:"queued_at"`
	StartedAt       time.Time `json:"started_at"`
	CompletedAt     time.Time `json:"completed_at"`
	RecoveryStarted bool      `json:"recovery_started"`
	PIIFindingCount int       `json:"pii_finding_count"`
	WasReclaimed    bool      `json:"was_reclaimed"`
	LastEventAt     time.Time `json:"last_event_at"`

	Attempts        []models.Attempt `json:"attempts"`
	AttemptCount    int
	LatestWorker    string
	FailureCategory string
	Duration        time.Duration

	PIIFindings []PIIFindingItem `json:"pii_findings"`

	ActiveAlertCount int `json:"active_alert_count"`

	Schedule *models.ScheduleDefinition `json:"schedule,omitempty"`

	Annotations []models.MonitoringAnnotation `json:"annotations,omitempty"`
}

type QueryFilter struct {
	Param  string
	Column string
}

func ApplyQueryFilters(c *gin.Context, query *gorm.DB, filters []QueryFilter) *gorm.DB {
	for _, filter := range filters {
		if value := c.Query(filter.Param); value != "" {
			query = query.Where(filter.Column+" = ?", value)
		}
	}

	return query
}

type QueryRangeFilter struct {
	Param  string
	Column string
	Op     string
}

func ApplyQueryRangeFilters(c *gin.Context, query *gorm.DB, filters []QueryRangeFilter) *gorm.DB {
	for _, filter := range filters {
		if value := c.Query(filter.Param); value != "" {
			query = query.Where(filter.Column+" "+filter.Op+" ?", value)
		}
	}

	return query
}

func applyRunDerivedFilters(c *gin.Context, query *gorm.DB) (*gorm.DB, error) {
	db := database.DB.WithContext(c.Request.Context())

	if value := c.Query("worker_id"); value != "" {
		subquery := db.Model(&models.Attempt{}).Select("job_id").Where("worker_id = ?", value)
		query = query.Where("job_id IN (?)", subquery)
	}

	if value := c.Query("started_time"); value != "" {
		subquery := db.Model(&models.RunProjection{}).Select("job_id").Where("started_at = ?", value)
		query = query.Where("job_id IN (?)", subquery)
	}

	if value := c.Query("started_from"); value != "" {
		subquery := db.Model(&models.RunProjection{}).Select("job_id").Where("started_at >= ?", value)
		query = query.Where("job_id IN (?)", subquery)
	}

	if value := c.Query("started_to"); value != "" {
		subquery := db.Model(&models.RunProjection{}).Select("job_id").Where("started_at <= ?", value)
		query = query.Where("job_id IN (?)", subquery)
	}

	if value := c.Query("attempt_count"); value != "" {
		if _, err := strconv.Atoi(value); err != nil {
			return query, fmt.Errorf("attempt_count must be a whole number")
		}
		subquery := db.Model(&models.Attempt{}).Select("job_id").Group("job_id").Having("COUNT(*) = ?", value)
		query = query.Where("job_id IN (?)", subquery)
	}

	if value := c.Query("failure_category"); value != "" {
		subquery := db.Model(&models.Attempt{}).Select("job_id").Where("failure_category = ?", value)
		query = query.Where("job_id IN (?)", subquery)
	}

	if value := c.Query("pii_type"); value != "" {
		subquery := db.Model(&models.PIIRecord{}).Select("job_id").Where("type = ?", value)
		query = query.Where("job_id IN (?)", subquery)
	}

	if value := c.Query("monitoring_annotation"); value != "" {
		subquery := db.Model(&models.MonitoringAnnotation{}).Select("subject_id").Where("subject_type = ? AND type = ?", "RUN", value)
		query = query.Where("job_id IN (?)", subquery)
	}

	if value := c.Query("alert_type"); value != "" {
		subquery := db.Model(&models.Alert{}).Select("subject_id").Where("subject_type = ? AND alert_type = ?", "RUN", value)
		query = query.Where("job_id IN (?)", subquery)
	}

	if value := c.Query("alert_severity"); value != "" {
		subquery := db.Model(&models.Alert{}).Select("subject_id").Where("subject_type = ? AND severity = ?", "RUN", value)
		query = query.Where("job_id IN (?)", subquery)
	}

	// duration_from / duration_to: validated as a plain number BEFORE being
	// concatenated into the interval string. GORM's "?" placeholder still
	// parameterizes it either way, but a non-numeric value here previously
	// produced a confusing Postgres error instead of a clean 400 — this
	// catches it at the Go layer where the message can actually say what's
	// wrong.
	if value := c.Query("duration_from"); value != "" {
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return query, fmt.Errorf("duration_from must be a number of seconds")
		}
		subquery := database.DB.
			Model(&models.Attempt{}).
			Select("job_id").
			Where("finished_at IS NOT NULL").
			Where("started_at IS NOT NULL").
			Where("finished_at - started_at >= (? || ' seconds')::interval", value)
		query = query.Where("job_id IN (?)", subquery)
	}

	if value := c.Query("duration_to"); value != "" {
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return query, fmt.Errorf("duration_to must be a number of seconds")
		}
		subquery := database.DB.
			Model(&models.Attempt{}).
			Select("job_id").
			Where("finished_at IS NOT NULL").
			Where("started_at IS NOT NULL").
			Where("finished_at - started_at <= (? || ' seconds')::interval", value)
		query = query.Where("job_id IN (?)", subquery)
	}

	return query, nil
}

func applyAlertDerivedFilters(c *gin.Context, query *gorm.DB) *gorm.DB {
	if value := c.Query("alert_type"); value != "" {
		types := strings.Split(value, ",")
		query = query.Where("alert_type IN ?", types)
	}

	return query
}

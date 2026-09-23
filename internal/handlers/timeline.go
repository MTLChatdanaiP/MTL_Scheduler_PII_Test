package handlers // or a new internal/timeline package if you prefer

import (
	"MTL_Scheduler_PII_Test/internal/auth"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
)

const timelineCap = 1000

// ---------------------------------------------------------------- the type

type TimelineEntry struct {
	OccurredAt time.Time              `json:"occurred_at"`
	EventType  string                 `json:"event_type"`
	RunID      string                 `json:"run_id"`
	Source     string                 `json:"source"` // EVENT|ATTEMPT|PII|ALERT|ANNOTATION
	Detail     map[string]interface{} `json:"detail,omitempty"`
}

// ---------------------------------------------------------------- builders
// one per source. pure functions — no DB calls inside these. testable with a
// plain slice in, no database needed.

func timelineFromEvents(events []models.EventEnvelope) []TimelineEntry {
	entries := make([]TimelineEntry, 0, len(events)) // entries  []TimelineEntry

	for _, event := range events {
		entry := TimelineEntry{OccurredAt: event.OccurredAt, EventType: event.EventType, RunID: event.JobId, Source: "EVENT"}
		entries = append(entries, entry)
	}

	return entries
}

func timelineFromAttempts(attempts []models.Attempt) []TimelineEntry {
	entries := make([]TimelineEntry, 0, len(attempts)*3)

	for _, attempt := range attempts {
		if !attempt.ClaimedAt.IsZero() {
			claimedDetail := map[string]interface{}{"worker_id": attempt.WorkerId, "attempt_number": attempt.AttemptNumber}
			entry := TimelineEntry{OccurredAt: attempt.ClaimedAt, EventType: "attempt.claimed", RunID: attempt.JobId, Source: "ATTEMPT", Detail: claimedDetail}
			entries = append(entries, entry)
		}
		if !attempt.StartedAt.IsZero() {
			startedDetail := map[string]interface{}{"worker_id": attempt.WorkerId, "attempt_number": attempt.AttemptNumber}
			entry := TimelineEntry{OccurredAt: attempt.StartedAt, EventType: "attempt.started", RunID: attempt.JobId, Source: "ATTEMPT", Detail: startedDetail}
			entries = append(entries, entry)
		}
		if !attempt.FinishedAt.IsZero() {
			finishedDetail := map[string]interface{}{"worker_id": attempt.WorkerId, "attempt_number": attempt.AttemptNumber, "status": attempt.Status, "failure_category": attempt.FailureCategory}
			entry := TimelineEntry{OccurredAt: attempt.FinishedAt, EventType: "attempt.finished", RunID: attempt.JobId, Source: "ATTEMPT", Detail: finishedDetail}
			entries = append(entries, entry)
		}
	}
	return entries
}

func timelineFromPII(records []models.PIIRecord) []TimelineEntry {
	entries := make([]TimelineEntry, 0, len(records))

	for _, record := range records {
		piiDetail := map[string]interface{}{"type": record.Type, "detector_id": record.DetectorID, "confidence": record.Confidence, "source": record.Source, "policy_action": record.PolicyAction}
		entry := TimelineEntry{OccurredAt: record.CreatedAt, EventType: "pii.detected", RunID: record.JobID, Source: "PII", Detail: piiDetail}
		entries = append(entries, entry)
	}

	return entries
}

func timelineFromAlerts(alerts []models.Alert) []TimelineEntry {
	entries := make([]TimelineEntry, 0, len(alerts)*3)

	for _, alert := range alerts {
		if alert.SubjectType != "RUN" {
			continue
		}
		alertDetail := map[string]interface{}{"severity": alert.Severity, "rule_id": alert.RuleID, "summary": alert.Summary}
		entry := TimelineEntry{OccurredAt: alert.OpenedAt, EventType: "alert.opened", RunID: alert.SubjectID, Source: "ALERT", Detail: alertDetail}
		entries = append(entries, entry)
		if alert.AcknowledgedAt != nil {
			alertDetail := map[string]interface{}{"severity": alert.Severity, "rule_id": alert.RuleID, "summary": alert.Summary}
			entry := TimelineEntry{OccurredAt: *alert.AcknowledgedAt, EventType: "alert.acknowledged", RunID: alert.SubjectID, Source: "ALERT", Detail: alertDetail}
			entries = append(entries, entry)
		}
		if alert.ResolvedAt != nil {
			alertDetail := map[string]interface{}{"severity": alert.Severity, "rule_id": alert.RuleID, "summary": alert.Summary}
			entry := TimelineEntry{OccurredAt: *alert.ResolvedAt, EventType: "alert.resolved", RunID: alert.SubjectID, Source: "ALERT", Detail: alertDetail}
			entries = append(entries, entry)
		}
	}

	return entries
}

func timelineFromAnnotations(annotations []models.MonitoringAnnotation) []TimelineEntry {
	entries := make([]TimelineEntry, 0, len(annotations)*2)

	for _, annotation := range annotations {
		if annotation.SubjectType != "TASK" {
			continue
		}
		annotationDetail := map[string]interface{}{"type": annotation.Type, "evidence": annotation.Evidence}
		entry := TimelineEntry{OccurredAt: annotation.DerivedAt, EventType: "annotation." + annotation.Type, RunID: annotation.SubjectID, Source: "ANNOTATION", Detail: annotationDetail}
		entries = append(entries, entry)
		if annotation.ResolvedAt != nil {
			annotationDetail := map[string]interface{}{"type": annotation.Type, "evidence": annotation.Evidence}
			entry := TimelineEntry{OccurredAt: *annotation.ResolvedAt, EventType: "annotation.resolved", RunID: annotation.SubjectID, Source: "ANNOTATION", Detail: annotationDetail}
			entries = append(entries, entry)
		}
	}
	return entries
}

// ---------------------------------------------------------------- assembler

func GetChainTimeline(c *gin.Context) {
	chainID := c.Param("execution_chain_id")

	entries, truncated, err := buildTimeline(c.Request.Context(), chainID, auth.HasScope(c, "pii.findings.read"))
	if err != nil {
		fmt.Println("[Database] Failed to build execution chain timeline:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build execution chain timeline"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"chain_id":  chainID,
		"entries":   entries,
		"count":     len(entries),
		"truncated": truncated,
	})
}

func GetRunTimeline(c *gin.Context) {
	runID := c.Param("run_id")

	var task models.Task
	if err := database.DB.WithContext(c.Request.Context()).Where("job_id = ?", runID).First(&task).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
		return
	}

	entries, truncated, err := buildTimeline(c.Request.Context(), task.ExecutionChainId, auth.HasScope(c, "pii.findings.read"))
	if err != nil {
		fmt.Println("[Database] Failed to build run timeline:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build run timeline"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"run_id":    runID,
		"entries":   entries,
		"count":     len(entries),
		"truncated": truncated,
	})
}

func buildTimeline(ctx context.Context, chainID string, canReadPII bool) ([]TimelineEntry, bool, error) {
	var jobIDs []string
	database.DB.WithContext(ctx).Model(&models.Task{}).Where("execution_chain_id = ?", chainID).Pluck("job_id", &jobIDs)
	if len(jobIDs) == 0 {
		var ErrChainNotFound = errors.New("execution chain not found")
		return nil, false, ErrChainNotFound
	}

	var events []models.EventEnvelope
	var attempts []models.Attempt
	var piiRecords []models.PIIRecord
	var alerts []models.Alert
	var annotations []models.MonitoringAnnotation

	if err := database.DB.WithContext(ctx).Where("job_id IN ?", jobIDs).Find(&events).Error; err != nil {
		fmt.Println("[Database] Failed to fetch run events:", err)
		return nil, false, err
	}

	if err := database.DB.WithContext(ctx).Where("job_id IN ?", jobIDs).Order("attempt_number ASC").Find(&attempts).Error; err != nil {
		fmt.Println("[Database] Failed to fetch run attempts:", err)
		return nil, false, err
	}

	if err := database.DB.WithContext(ctx).Where("job_id IN ?", jobIDs).Find(&piiRecords).Error; err != nil {
		fmt.Println("[Database] Failed to fetch PII findings:", err)
		return nil, false, err
	}

	if err := database.DB.WithContext(ctx).Where("subject_type = ? AND subject_id IN ?", "RUN", jobIDs).Find(&alerts).Error; err != nil {
		fmt.Println("[Database] Failed to fetch run alerts:", err)
		return nil, false, err
	}

	if err := database.DB.WithContext(ctx).Where("subject_type = ? AND subject_id IN ?", "RUN", jobIDs).Find(&annotations).Error; err != nil {
		fmt.Println("[Database] Failed to fetch run annotations:", err)

		return nil, false, err
	}

	var all []TimelineEntry // all  []TimelineEntry
	all = append(all, timelineFromEvents(events)...)
	all = append(all, timelineFromAttempts(attempts)...)
	all = append(all, timelineFromPII(piiRecords)...)
	all = append(all, timelineFromAlerts(alerts)...)
	all = append(all, timelineFromAnnotations(annotations)...)

	if !canReadPII {
		filtered := make([]TimelineEntry, 0, len(all))
		for _, entry := range all {
			if entry.Source != "PII" {
				filtered = append(filtered, entry)
			}
		}
		all = filtered
	}

	sort.SliceStable(all, func(i, j int) bool {
		return all[i].OccurredAt.Before(all[j].OccurredAt)
	})

	truncated := false
	if len(all) > timelineCap {
		all = all[:timelineCap]
		truncated = true
	}

	return all, truncated, nil
}

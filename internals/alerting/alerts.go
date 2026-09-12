package alerts

import (
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/events"
	"MTL_Scheduler_PII_Test/internals/models"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

const (
	alertsInterval          = 20 * time.Second
	maxNotificationAttempts = 3
	notificationRetryDelay  = 5 * time.Minute
	notificationBatchLimit  = 50
)

func RunOpenPass(ctx context.Context)          { openAlertsFromAnnotations(ctx) }
func RunResolvePass(ctx context.Context)       { resolveAlertsFromAnnotations(ctx) }
func RunMetricOpenPass(ctx context.Context)    { openAlertsFromMetrics(ctx) }
func RunMetricResolvePass(ctx context.Context) { resolveAlertsFromMetrics(ctx) }
func RunNotificationPass(ctx context.Context)  { sendPendingNotifications(ctx) }

func subjectTypeFor(annotationSubjectType string) string {
	if annotationSubjectType == "TASK" {
		return "RUN"
	}
	return annotationSubjectType
}

func summaryFor(alertType string, subjectType string, subjectID string) string {
	return fmt.Sprintf("%s on %s %s", alertType, subjectType, subjectID)
}

func StartAlertsSweep(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		openAlertsFromAnnotations(ctx)
		openAlertsFromMetrics(ctx)
		resolveAlertsFromAnnotations(ctx)
		resolveAlertsFromMetrics(ctx)
		sendPendingNotifications(ctx)

		time.Sleep(alertsInterval)
	}
}

func openAlertsFromAnnotations(ctx context.Context) {

	var MAs []models.MonitoringAnnotation
	if err := database.DB.WithContext(ctx).Where("resolved_at IS NULL").Find(&MAs).Error; err != nil {
		fmt.Println("alerting: failed to query unresolved annotations:", err)
		return
	}

	rules := GetLoadedRules()

	for _, MA := range MAs {
		rule, ok := annotationFindRuleFromType(rules, MA.Type)

		if !ok {
			continue
		}

		var ExistingAlert models.Alert
		if err := database.DB.WithContext(ctx).Where("source_annotation_id = ? AND status != ?", MA.AnnotationID, "RESOLVED").First(&ExistingAlert).Error; err == nil {
			updateSeverityIfChanged(ctx, ExistingAlert, rule)
			continue
		}

		translatedSubjectType := subjectTypeFor(MA.SubjectType)

		alert := models.Alert{
			AlertID:            ulid.Make().String(),
			AlertType:          rule.AlertType,
			Status:             "OPEN",
			OpenedAt:           time.Now().UTC(),
			Severity:           rule.Severity,
			Evidence:           MA.Evidence,
			SourceAnnotationID: MA.AnnotationID,
			SubjectID:          MA.SubjectID,
			SubjectType:        translatedSubjectType,
			Summary:            summaryFor(MA.Type, translatedSubjectType, MA.SubjectID),
			RuleID:             rule.ID,
			RuleVersion:        rules.Metadata.Version,
		}

		if err := database.DB.WithContext(ctx).Create(&alert).Error; err != nil {
			fmt.Println("alerting: failed to create alert for annotation", MA.AnnotationID, ":", err)
			continue
		}

		events.LogEvent(ctx, alert.SubjectID, "alert.opened", "alerting")

		createNotifications(ctx, alert)
	}
}

func resolveAlertsFromAnnotations(ctx context.Context) {
	var ExistingAlerts []models.Alert
	if err := database.DB.WithContext(ctx).Where("status != ?", "RESOLVED").Find(&ExistingAlerts).Error; err != nil {
		fmt.Println("alerting: failed to query open alerts:", err)
		return
	}

	for _, alert := range ExistingAlerts {
		if alert.SourceAnnotationID == "" {
			fmt.Println("alerting: alert", alert.AlertID, "has no source annotation, skipping")
			continue
		}
		var MA models.MonitoringAnnotation
		if err := database.DB.WithContext(ctx).Where("annotation_id = ?", alert.SourceAnnotationID).First(&MA).Error; err != nil {
			fmt.Println("alerting: could not find annotation", alert.SourceAnnotationID, "for alert", alert.AlertID, ":", err)
			continue
		}
		if MA.ResolvedAt == nil {
			continue
		}

		now := time.Now().UTC()
		alert.ResolvedAt = &now
		alert.Status = "RESOLVED"

		if err := database.DB.WithContext(ctx).Save(&alert).Error; err != nil {
			fmt.Println("alerting: failed to resolve alert", alert.AlertID, ":", err)
			continue
		}

		events.LogEvent(ctx, alert.SubjectID, "alert.resolved", "alerting")
	}
}

func Acknowledge(ctx context.Context, alertID string, actor string) error {
	var alert models.Alert
	if err := database.DB.WithContext(ctx).Where("alert_id = ?", alertID).First(&alert).Error; err != nil {
		fmt.Println("failed to look up alert: ", err)
		return err
	}
	if alert.Status != "OPEN" {
		return fmt.Errorf("alert %s is %s, only OPEN alerts can be acknowledged", alertID, alert.Status)
	}
	alert.Status = "ACKNOWLEDGED"
	now := time.Now().UTC()
	alert.AcknowledgedAt = &now
	alert.AcknowledgedBy = actor
	if err := database.DB.WithContext(ctx).Save(&alert).Error; err != nil {
		return fmt.Errorf("failed to acknowledge alert %s: %w", alertID, err)
	}
	events.LogEvent(ctx, alert.SubjectID, "alert.acknowledged", "alerting")
	return nil
}

func openAlertsFromMetrics(ctx context.Context) {

	rules := GetLoadedRules()

	for _, rule := range rules.Spec.Rules {
		if !rule.Enabled || rule.Source != "METRIC" {
			continue
		}

		resolver, ok := MetricResolvers[rule.Metric]
		if !ok {
			fmt.Println("alerting: rule", rule.ID, "names unknown metric", rule.Metric)
			continue
		}

		samples, err := resolver(ctx)
		if err != nil {
			fmt.Println("alerting: rule", rule.ID, "failed to resolve metric", rule.Metric, ":", err)
			continue
		}

		for _, sample := range samples {
			if !scopeMatches(rule.Scope, sample) || !conditionMatches(rule, sample) {
				continue
			}

			var existing models.Alert
			if err := database.DB.WithContext(ctx).
				Where("rule_id = ? AND subject_type = ? AND subject_id = ? AND status != ?",
					rule.ID, sample.SubjectType, sample.SubjectID, "RESOLVED").
				First(&existing).Error; err == nil {
				updateSeverityIfChanged(ctx, existing, rule)
				continue
			}

			enriched := make(map[string]interface{}, len(sample.Evidence)+2)
			for k, v := range sample.Evidence {
				enriched[k] = v
			}
			enriched["operator"] = rule.Operator
			if rule.Operator == "EQ" || rule.Operator == "NEQ" {
				enriched["threshold"] = rule.TextValue
			} else {
				enriched["threshold"] = rule.Threshold
			}

			evidence := "{}"
			if data, err := json.Marshal(enriched); err == nil {
				evidence = string(data)
			}

			alert := models.Alert{
				AlertID:     ulid.Make().String(),
				AlertType:   rule.AlertType,
				Status:      "OPEN",
				OpenedAt:    time.Now().UTC(),
				Severity:    rule.Severity,
				Evidence:    evidence,
				SubjectID:   sample.SubjectID,
				SubjectType: sample.SubjectType,
				Summary:     summaryFor(rule.AlertType, sample.SubjectType, sample.SubjectID),
				RuleID:      rule.ID,
				RuleVersion: rules.Metadata.Version,
			}

			if err := database.DB.WithContext(ctx).Create(&alert).Error; err != nil {
				fmt.Println("alerting: failed to create alert for rule", rule.ID, "subject", sample.SubjectID, ":", err)
				continue
			}

			events.LogEvent(ctx, alert.SubjectID, "alert.opened", "alerting")

			createNotifications(ctx, alert)
		}
	}
}

func resolveAlertsFromMetrics(ctx context.Context) {
	var existingAlerts []models.Alert

	if err := database.DB.WithContext(ctx).
		Where("status != ? AND source_annotation_id = ?", "RESOLVED", "").
		Find(&existingAlerts).Error; err != nil {
		fmt.Println("alerting: failed to query open metric alerts:", err)
		return
	}

	rulesDoc := GetLoadedRules()

	sampleCache := make(map[string][]MetricSample)

	for _, alert := range existingAlerts {
		rule, found := findRuleByID(rulesDoc, alert.RuleID)

		if !found || !rule.Enabled || rule.Source != "METRIC" {
			continue
		}

		samples, cached := sampleCache[rule.Metric]
		if !cached {
			resolver, ok := MetricResolvers[rule.Metric]
			if !ok {
				fmt.Println("alerting: alert", alert.AlertID, "references unknown metric", rule.Metric)
				continue
			}

			resolved, err := resolver(ctx)
			if err != nil {
				fmt.Println("alerting: failed to resolve metric", rule.Metric, "for alert", alert.AlertID, ":", err)
				continue
			}

			samples = resolved
			sampleCache[rule.Metric] = resolved
		}

		stillBreaching := false
		sampleExists := false

		for _, sample := range samples {
			if sample.SubjectType != alert.SubjectType || sample.SubjectID != alert.SubjectID {
				continue
			}

			sampleExists = true

			if scopeMatches(rule.Scope, sample) && conditionMatches(rule, sample) {
				stillBreaching = true
			}
			break
		}

		if !sampleExists || stillBreaching {
			continue
		}

		now := time.Now().UTC()
		alert.Status = "RESOLVED"
		alert.ResolvedAt = &now

		if err := database.DB.WithContext(ctx).Save(&alert).Error; err != nil {
			fmt.Println("alerting: failed to resolve alert", alert.AlertID, ":", err)
			continue
		}

		events.LogEvent(ctx, alert.SubjectID, "alert.resolved", "alerting")
	}
}

func findRuleByID(rulesDoc models.AlertRules, ruleID string) (models.AlertRule, bool) {
	for _, rule := range rulesDoc.Spec.Rules {
		if rule.ID == ruleID {
			return rule, true
		}
	}
	return models.AlertRule{}, false
}

func updateSeverityIfChanged(ctx context.Context, existing models.Alert, rule models.AlertRule) {
	if existing.Severity == rule.Severity {
		return
	}

	previous := existing.Severity

	existing.Severity = rule.Severity

	if err := database.DB.WithContext(ctx).Save(&existing).Error; err != nil {
		fmt.Println("alerting: failed to update severity for alert", existing.AlertID, ":", err)
		return
	}

	fmt.Println("alerting: alert", existing.AlertID, "severity", previous, "->", rule.Severity)

	// RFC-007 §14's alert.updated event.
	events.LogEvent(ctx, existing.SubjectID, "alert.updated", "alerting")
}

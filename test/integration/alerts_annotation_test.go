package integration_test

// RFC-007 §6 Lifecycle, §9 Deduplication, §11 Resolution.
//
// This is the automated version of the seeded-annotation test run by hand:
// insert an annotation, watch an alert open, acknowledge it, resolve the
// annotation, watch the alert follow.
//
// The sweep functions are unexported, so this drives the pipeline the same way
// the sweep does -- by calling the exported entry points and inspecting the
// database. If openAlertsFromAnnotations and resolveAlertsFromAnnotations are
// unexported in your build, either export thin test wrappers or move this file
// into internals/alerting as an in-package test. The assertions do not change.

import (
	"context"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	alerts "MTL_Scheduler_PII_Test/internals/alerting"
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
)

// seedAnnotation inserts one unresolved monitoring fact and registers cleanup
// for it and anything derived from it.
func seedAnnotation(t *testing.T, annotationType string, subjectType string) models.MonitoringAnnotation {
	t.Helper()

	annotation := models.MonitoringAnnotation{
		AnnotationID: ulid.Make().String(),
		Type:         annotationType,
		SubjectType:  subjectType,
		SubjectID:    "test-subject-" + ulid.Make().String(),
		DerivedAt:    time.Now().UTC(),
		Evidence:     `{"threshold_seconds":60}`,
	}

	if err := database.DB.Create(&annotation).Error; err != nil {
		t.Fatalf("failed to seed annotation: %v", err)
	}

	t.Cleanup(func() {
		database.DB.Where("source_annotation_id = ?", annotation.AnnotationID).Delete(&models.Alert{})
		database.DB.Where("annotation_id = ?", annotation.AnnotationID).Delete(&models.MonitoringAnnotation{})
	})

	return annotation
}

func alertFor(t *testing.T, annotationID string) models.Alert {
	t.Helper()

	var alert models.Alert
	if err := database.DB.Where("source_annotation_id = ?", annotationID).First(&alert).Error; err != nil {
		t.Fatalf("no alert found for annotation %s: %v", annotationID, err)
	}
	return alert
}

func TestAlerting_OpensAlertFromAnnotation(t *testing.T) {
	ctx := context.Background()

	annotation := seedAnnotation(t, "RUN_STUCK", "TASK")

	alerts.RunOpenPass(ctx)

	alert := alertFor(t, annotation.AnnotationID)

	if alert.Status != "OPEN" {
		t.Errorf("status = %q, want OPEN", alert.Status)
	}
	if alert.AlertType != "RUN_STUCK" {
		t.Errorf("alert type = %q, want RUN_STUCK", alert.AlertType)
	}
	if alert.Severity == "" {
		t.Error("severity is empty -- severityFor did not run or returned nothing")
	}

	// The vocabulary translation at the boundary: the annotation says TASK,
	// RFC-007 §7's subject list says RUN.
	if alert.SubjectType != "RUN" {
		t.Errorf("subject type = %q, want RUN (translated from TASK)", alert.SubjectType)
	}
	if alert.SubjectID != annotation.SubjectID {
		t.Errorf("subject id = %q, want %q", alert.SubjectID, annotation.SubjectID)
	}

	// Fields that were unset for a while and produced alerts nobody could act on.
	if alert.Summary == "" {
		t.Error("summary is empty")
	}
	if alert.RuleID == "" {
		t.Error("rule id is empty")
	}

	// Evidence is carried across from the monitoring fact, not recomputed --
	// recomputing it would be RFC-007 §3's non-goal.
	if alert.Evidence != annotation.Evidence {
		t.Errorf("evidence = %q, want it copied verbatim from the annotation (%q)", alert.Evidence, annotation.Evidence)
	}
}

func TestAlerting_DoesNotDuplicateOnRepeatedSweeps(t *testing.T) {
	ctx := context.Background()

	annotation := seedAnnotation(t, "RUN_STUCK", "TASK")

	// Three passes over the same unresolved fact.
	alerts.RunOpenPass(ctx)
	alerts.RunOpenPass(ctx)
	alerts.RunOpenPass(ctx)

	var count int64
	database.DB.Model(&models.Alert{}).
		Where("source_annotation_id = ?", annotation.AnnotationID).
		Count(&count)

	if count != 1 {
		t.Errorf("expected exactly 1 alert after 3 sweeps, got %d", count)
	}
}

func TestAlerting_AcknowledgedAlertIsNotDuplicated(t *testing.T) {
	ctx := context.Background()

	annotation := seedAnnotation(t, "RUN_STUCK", "TASK")

	alerts.RunOpenPass(ctx)
	alert := alertFor(t, annotation.AnnotationID)

	if err := alerts.Acknowledge(ctx, alert.AlertID, "test-actor"); err != nil {
		t.Fatalf("Acknowledge failed: %v", err)
	}

	// THE CASE THAT JUSTIFIES THE DEDUP QUERY'S SHAPE.
	// An acknowledged alert still has a NULL resolved_at. If dedup filtered on
	// "resolved_at IS NULL" instead of "status != RESOLVED", this sweep would
	// treat the acknowledged alert as absent and open a duplicate.
	alerts.RunOpenPass(ctx)

	var count int64
	database.DB.Model(&models.Alert{}).
		Where("source_annotation_id = ?", annotation.AnnotationID).
		Count(&count)

	if count != 1 {
		t.Errorf("expected 1 alert after acknowledging and re-sweeping, got %d -- dedup is probably filtering on resolved_at rather than status", count)
	}
}

func TestAlerting_AcknowledgeRecordsActor(t *testing.T) {
	ctx := context.Background()

	annotation := seedAnnotation(t, "SCHEDULE_MISSED", "SCHEDULE")

	alerts.RunOpenPass(ctx)
	alert := alertFor(t, annotation.AnnotationID)

	if err := alerts.Acknowledge(ctx, alert.AlertID, "test-actor"); err != nil {
		t.Fatalf("Acknowledge failed: %v", err)
	}

	acked := alertFor(t, annotation.AnnotationID)

	if acked.Status != "ACKNOWLEDGED" {
		t.Errorf("status = %q, want ACKNOWLEDGED", acked.Status)
	}
	if acked.AcknowledgedBy != "test-actor" {
		t.Errorf("acknowledged_by = %q, want test-actor", acked.AcknowledgedBy)
	}
	if acked.AcknowledgedAt == nil {
		t.Error("acknowledged_at is nil")
	}

	// QUEUE is already RFC-007 vocabulary, so it must pass through untranslated.
	if acked.SubjectType != "QUEUE" {
		t.Errorf("subject type = %q, want QUEUE unchanged", acked.SubjectType)
	}
}

func TestAlerting_RejectsAcknowledgingTwice(t *testing.T) {
	ctx := context.Background()

	annotation := seedAnnotation(t, "RUN_STUCK", "TASK")

	alerts.RunOpenPass(ctx)
	alert := alertFor(t, annotation.AnnotationID)

	if err := alerts.Acknowledge(ctx, alert.AlertID, "first-actor"); err != nil {
		t.Fatalf("first Acknowledge failed: %v", err)
	}

	// A second acknowledgement must be a clear refusal rather than a silent
	// no-op -- otherwise the second actor's name would quietly overwrite the
	// first, losing who actually responded.
	if err := alerts.Acknowledge(ctx, alert.AlertID, "second-actor"); err == nil {
		t.Error("acknowledging an already-acknowledged alert succeeded, expected an error")
	}

	after := alertFor(t, annotation.AnnotationID)
	if after.AcknowledgedBy != "first-actor" {
		t.Errorf("acknowledged_by = %q, want it to remain first-actor", after.AcknowledgedBy)
	}
}

func TestAlerting_RejectsAcknowledgingUnknownAlert(t *testing.T) {
	if err := alerts.Acknowledge(context.Background(), "no-such-alert-id", "test-actor"); err == nil {
		t.Error("acknowledging a nonexistent alert succeeded, expected an error")
	}
}

func TestAlerting_ResolvesWhenAnnotationResolves(t *testing.T) {
	ctx := context.Background()

	annotation := seedAnnotation(t, "RUN_STUCK", "TASK")

	alerts.RunOpenPass(ctx)
	alert := alertFor(t, annotation.AnnotationID)

	// Go through ACKNOWLEDGED rather than straight from OPEN: that exercises the
	// fuller lifecycle path and checks the acknowledgement details survive
	// resolution.
	if err := alerts.Acknowledge(ctx, alert.AlertID, "test-actor"); err != nil {
		t.Fatalf("Acknowledge failed: %v", err)
	}

	// Monitoring decides the condition has cleared. Alerting only reads that
	// verdict -- it must not re-evaluate the condition itself.
	now := time.Now().UTC()
	database.DB.Model(&models.MonitoringAnnotation{}).
		Where("annotation_id = ?", annotation.AnnotationID).
		Update("resolved_at", &now)

	alerts.RunResolvePass(ctx)

	resolved := alertFor(t, annotation.AnnotationID)

	if resolved.Status != "RESOLVED" {
		t.Errorf("status = %q, want RESOLVED", resolved.Status)
	}
	if resolved.ResolvedAt == nil {
		t.Error("resolved_at is nil")
	}

	// RFC-007 §11: "a historical resolution should not erase the previous
	// alert." The row is updated, not replaced, so the acknowledgement survives.
	if resolved.AcknowledgedBy != "test-actor" {
		t.Errorf("acknowledged_by = %q, want it preserved through resolution", resolved.AcknowledgedBy)
	}
	if resolved.AcknowledgedAt == nil {
		t.Error("acknowledged_at was cleared by resolution")
	}
	if resolved.OpenedAt.IsZero() {
		t.Error("opened_at was cleared by resolution -- the true duration of the problem is lost")
	}
}

func TestAlerting_LeavesAlertOpenWhileAnnotationUnresolved(t *testing.T) {
	ctx := context.Background()

	annotation := seedAnnotation(t, "RUN_STUCK", "TASK")

	alerts.RunOpenPass(ctx)
	alerts.RunResolvePass(ctx)

	alert := alertFor(t, annotation.AnnotationID)

	if alert.Status != "OPEN" {
		t.Errorf("status = %q, want OPEN -- the condition is still true", alert.Status)
	}
	if alert.ResolvedAt != nil {
		t.Error("resolved_at was set while the annotation is still unresolved")
	}
}

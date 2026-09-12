package integration_test

// RFC-007 §8 — metric-derived alerts, §9 dedup, §11 resolution.
//
// Mirrors the annotation lifecycle tests, but seeded with a heartbeat instead
// of an annotation. These depend on rules.json containing
// worker-offline-default: source METRIC, metric worker.heartbeat_age,
// operator GT, threshold 60, GLOBAL scope. If that rule is renamed or its
// threshold changed, the seeded ages below need to change with it.

import (
	"context"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	alerts "MTL_Scheduler_PII_Test/internals/alerting"
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
)

const workerOfflineRuleID = "worker-offline-default"

// metricAlertFor finds the alert for one worker under the offline rule.
// Scoped by rule and subject so it cannot pick up an alert left behind by a
// running app or another test.
func metricAlertFor(t *testing.T, workerID string) (models.Alert, bool) {
	t.Helper()

	var alert models.Alert
	err := database.DB.
		Where("rule_id = ? AND subject_id = ?", workerOfflineRuleID, workerID).
		First(&alert).Error

	if err != nil {
		return models.Alert{}, false
	}
	return alert, true
}

func countMetricAlerts(t *testing.T, workerID string) int64 {
	t.Helper()

	var count int64
	database.DB.Model(&models.Alert{}).
		Where("rule_id = ? AND subject_id = ?", workerOfflineRuleID, workerID).
		Count(&count)
	return count
}

// newOfflineWorker seeds a worker whose newest heartbeat is old enough to
// breach the 60s threshold, and cleans up both the heartbeats and any alert
// derived from them.
func newOfflineWorker(t *testing.T) string {
	t.Helper()

	workerID := "test-worker-" + ulid.Make().String()
	seedHeartbeat(t, workerID, 5*time.Minute, 0, 5)

	t.Cleanup(func() {
		database.DB.Where("subject_id = ?", workerID).Delete(&models.Alert{})
	})

	return workerID
}

func TestMetricAlert_OpensWhenThresholdBreached(t *testing.T) {
	ctx := context.Background()
	workerID := newOfflineWorker(t)

	alerts.RunMetricOpenPass(ctx)

	alert, ok := metricAlertFor(t, workerID)
	if !ok {
		t.Fatalf("no alert opened for offline worker %q", workerID)
	}

	if alert.AlertType != "WORKER_OFFLINE" {
		t.Errorf("alert type = %q, want WORKER_OFFLINE", alert.AlertType)
	}
	if alert.Status != "OPEN" {
		t.Errorf("status = %q, want OPEN", alert.Status)
	}
	if alert.Severity == "" {
		t.Error("severity is empty -- it should come from the rule")
	}
	if alert.RuleID != workerOfflineRuleID {
		t.Errorf("rule id = %q, want %q", alert.RuleID, workerOfflineRuleID)
	}

	// THE FIELD THAT DISTINGUISHES A METRIC ALERT FROM AN ANNOTATION ONE.
	// Both resolve passes filter on it: the annotation resolver skips alerts
	// without a source annotation, and the metric resolver only handles alerts
	// with an empty one. If this were populated, this alert would be handled by
	// the wrong resolver -- or by both.
	if alert.SourceAnnotationID != "" {
		t.Errorf("source annotation id = %q, want empty for a metric alert", alert.SourceAnnotationID)
	}

	// Metric resolvers emit RFC-007 §7 vocabulary directly -- no subjectTypeFor
	// call exists anywhere in the metric path.
	if alert.SubjectType != "WORKER" {
		t.Errorf("subject type = %q, want WORKER", alert.SubjectType)
	}

	if alert.Evidence == "" || alert.Evidence == "{}" {
		t.Errorf("evidence = %q, want the sample's evidence map", alert.Evidence)
	}
	if alert.Summary == "" {
		t.Error("summary is empty")
	}
}

func TestMetricAlert_DoesNotOpenWhenBelowThreshold(t *testing.T) {
	ctx := context.Background()

	workerID := "test-worker-healthy-" + ulid.Make().String()
	seedHeartbeat(t, workerID, 5*time.Second, 1, 5) // well under 60s

	t.Cleanup(func() {
		database.DB.Where("subject_id = ?", workerID).Delete(&models.Alert{})
	})

	alerts.RunMetricOpenPass(ctx)

	if _, ok := metricAlertFor(t, workerID); ok {
		t.Error("an alert was opened for a worker that reported 5 seconds ago")
	}
}

func TestMetricAlert_DoesNotDuplicateOnRepeatedSweeps(t *testing.T) {
	ctx := context.Background()
	workerID := newOfflineWorker(t)

	alerts.RunMetricOpenPass(ctx)
	alerts.RunMetricOpenPass(ctx)
	alerts.RunMetricOpenPass(ctx)

	// Without dedup this opens a new alert every tick, forever, for as long as
	// the worker stays offline.
	if got := countMetricAlerts(t, workerID); got != 1 {
		t.Errorf("expected exactly 1 alert after 3 sweeps, got %d", got)
	}
}

func TestMetricAlert_AcknowledgedAlertIsNotDuplicated(t *testing.T) {
	ctx := context.Background()
	workerID := newOfflineWorker(t)

	alerts.RunMetricOpenPass(ctx)

	alert, ok := metricAlertFor(t, workerID)
	if !ok {
		t.Fatal("setup failed: no alert opened")
	}

	if err := alerts.Acknowledge(ctx, alert.AlertID, "test-actor"); err != nil {
		t.Fatalf("Acknowledge failed: %v", err)
	}

	// THE CASE THAT JUSTIFIES THE DEDUP QUERY'S SHAPE, metric equivalent of the
	// annotation test. An acknowledged alert still has a NULL resolved_at, so a
	// dedup filtering on "resolved_at IS NULL" would treat it as absent and open
	// a duplicate during the acknowledged window.
	alerts.RunMetricOpenPass(ctx)

	if got := countMetricAlerts(t, workerID); got != 1 {
		t.Errorf("expected 1 alert after acknowledging and re-sweeping, got %d -- dedup is probably filtering on resolved_at rather than status", got)
	}
}

func TestMetricAlert_SeparateAlertPerWorker(t *testing.T) {
	ctx := context.Background()

	workerA := newOfflineWorker(t)
	workerB := newOfflineWorker(t)

	alerts.RunMetricOpenPass(ctx)

	// The dedup key includes subject_id precisely so one rule can hold an open
	// alert per worker. Without it, the first worker to go offline would
	// suppress alerts for every other worker under the same rule.
	if _, ok := metricAlertFor(t, workerA); !ok {
		t.Error("no alert for worker A")
	}
	if _, ok := metricAlertFor(t, workerB); !ok {
		t.Error("no alert for worker B -- one rule should hold one alert per subject")
	}
}

func TestMetricAlert_ResolvesWhenConditionClears(t *testing.T) {
	ctx := context.Background()
	workerID := newOfflineWorker(t)

	alerts.RunMetricOpenPass(ctx)

	if _, ok := metricAlertFor(t, workerID); !ok {
		t.Fatal("setup failed: no alert opened")
	}

	// The worker comes back. Its newest heartbeat is now recent, so the metric
	// no longer breaches -- note nothing "resolves" this explicitly the way an
	// annotation's resolved_at does; the resolve pass re-checks and decides.
	seedHeartbeat(t, workerID, 2*time.Second, 1, 5)

	alerts.RunMetricResolvePass(ctx)

	alert, ok := metricAlertFor(t, workerID)
	if !ok {
		t.Fatal("alert disappeared -- resolution must UPDATE the row, not delete it")
	}

	if alert.Status != "RESOLVED" {
		t.Errorf("status = %q, want RESOLVED", alert.Status)
	}
	if alert.ResolvedAt == nil {
		t.Error("resolved_at is nil")
	}
	if alert.OpenedAt.IsZero() {
		t.Error("opened_at was cleared -- the true duration of the incident is lost")
	}
}

func TestMetricAlert_StaysOpenWhileStillBreaching(t *testing.T) {
	ctx := context.Background()
	workerID := newOfflineWorker(t)

	alerts.RunMetricOpenPass(ctx)
	alerts.RunMetricResolvePass(ctx)

	alert, ok := metricAlertFor(t, workerID)
	if !ok {
		t.Fatal("alert disappeared")
	}

	if alert.Status != "OPEN" {
		t.Errorf("status = %q, want OPEN -- the worker is still offline", alert.Status)
	}
}

func TestMetricAlert_StaysOpenWhenSubjectHasNoSample(t *testing.T) {
	ctx := context.Background()
	workerID := newOfflineWorker(t)

	alerts.RunMetricOpenPass(ctx)

	if _, ok := metricAlertFor(t, workerID); !ok {
		t.Fatal("setup failed: no alert opened")
	}

	// The worker vanishes completely -- no heartbeat rows at all. This is what a
	// decommissioned or long-dead worker looks like once its last heartbeat
	// falls outside the resolver's query window.
	database.DB.Where("worker_id = ?", workerID).Delete(&models.WorkerHeartbeat{})

	alerts.RunMetricResolvePass(ctx)

	alert, ok := metricAlertFor(t, workerID)
	if !ok {
		t.Fatal("alert disappeared")
	}

	// THE CASE MOST LIKELY TO BE IMPLEMENTED BACKWARDS.
	//
	// "No sample" is not "no longer breaching". For WORKER_OFFLINE the absence
	// of a heartbeat IS the condition, so resolving here would close the alert
	// at exactly the moment the problem got worse -- and the alert list would
	// look healthiest when the fleet was most broken.
	if alert.Status != "OPEN" {
		t.Errorf("status = %q, want OPEN -- a subject with no sample must not be treated as recovered", alert.Status)
	}
}

func TestMetricAlert_ResolvePassIgnoresAnnotationAlerts(t *testing.T) {
	ctx := context.Background()

	annotation := seedAnnotation(t, "RUN_STUCK", "TASK")

	alerts.RunOpenPass(ctx)

	// The metric resolve pass filters on an empty source_annotation_id. If that
	// filter were missing, this pass would find the annotation alert, fail to
	// match it to a metric rule, and the two resolvers would fight over the same
	// rows -- each deciding the other's alerts should stay open.
	alerts.RunMetricResolvePass(ctx)

	alert := alertFor(t, annotation.AnnotationID)

	if alert.Status != "OPEN" {
		t.Errorf("status = %q -- the metric resolve pass touched an annotation-derived alert", alert.Status)
	}
}

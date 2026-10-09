package alerts

// RFC-007 §4 Alert Types. Database-free, like the rest of this package's tests.

import (
	"strings"
	"testing"

	"MTL_Scheduler_PII_Test/internal/models"
)

func metricRule(id, metric, operator string) models.AlertRule {
	return models.AlertRule{
		ID: id, Enabled: true, Source: "METRIC", Metric: metric, Operator: operator, Threshold: 900,
		AlertType: "RUN_FAILED", Severity: "WARNING", Scope: models.RuleScope{Type: "GLOBAL"},
	}
}

// A typo in a metric name or an operator used to fail nowhere: the rule loaded, looked fine, and never fired.
func TestValidateRules_RejectsTyposThatMakeARuleSilentlyNeverFire(t *testing.T) {
	tests := []struct {
		name     string
		rule     models.AlertRule
		contains string
	}{
		{"unknown metric", metricRule("r1", "run.faild_age", "LTE"), "unknown metric"},
		{"unknown operator", metricRule("r1", "run.failed_age", "<="), "unknown operator"},
		{"empty operator", metricRule("r1", "run.failed_age", ""), "unknown operator"},
		{"EQ with nothing to compare to", metricRule("r1", "queue.consumer_count", "EQ"), "textValue"},
		{"NEQ with nothing to compare to", metricRule("r1", "monitoring.status", "NEQ"), "textValue"},
		{"annotation rule with no type", models.AlertRule{ID: "r1", Enabled: true, Source: "ANNOTATION", AlertType: "RUN_STUCK", Severity: "CRITICAL", Scope: models.RuleScope{Type: "GLOBAL"}}, "annotationType"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems := ValidateRules(rulesDoc(tt.rule))
			if len(problems) != 1 {
				t.Fatalf("expected exactly 1 problem, got %d: %v", len(problems), problems)
			}
			if !strings.Contains(problems[0], tt.contains) || !strings.Contains(problems[0], `"r1"`) {
				t.Errorf("message %q should name the rule and mention %q", problems[0], tt.contains)
			}
		})
	}
}

func TestValidateRules_AcceptsEveryOperatorAndMetricThatWorks(t *testing.T) {
	for _, op := range []string{"GT", "GTE", "LT", "LTE"} {
		if p := ValidateRules(rulesDoc(metricRule("ok", "run.failed_age", op))); len(p) != 0 {
			t.Errorf("operator %s should be valid: %v", op, p)
		}
	}
	eq := metricRule("ok", "queue.consumer_count", "EQ")
	eq.TextValue = "0"
	if p := ValidateRules(rulesDoc(eq)); len(p) != 0 {
		t.Errorf("EQ with a textValue should be valid: %v", p)
	}
}

func TestMetricResolvers_EveryNewMetricIsRegistered(t *testing.T) {
	for _, name := range []string{
		"run.failed_age", "run.retries_exhausted_age", "run.timed_out_age", "pii.scan_failed_age", "schedule.creation_lateness",
		// the seven that existed before
		"worker.heartbeat_age", "worker.capacity_used", "queue.oldest_age", "queue.pending_count", "queue.consumer_count", "monitoring.status", "pii.policy_action",
	} {
		if MetricResolvers[name] == nil {
			t.Errorf("metric %q has no resolver, so any rule using it is rejected", name)
		}
	}
}

// "Alert while it is recent": LTE 900 fires for 15 minutes after the event, then stops by itself.
func TestConditionMatches_AnAgeRuleFiresWhileTheEventIsRecentThenStops(t *testing.T) {
	rule := metricRule("r1", "run.failed_age", "LTE")

	for _, tt := range []struct {
		age  float64
		want bool
	}{{0, true}, {60, true}, {900, true}, {900.5, false}, {3600, false}} {
		if got := conditionMatches(rule, MetricSample{Numeric: tt.age}); got != tt.want {
			t.Errorf("age %.1fs: fires = %v, want %v", tt.age, got, tt.want)
		}
	}
}

// the rule set a deployment should end up with covers every type RFC-007 §4 names
var rfcAlertTypes = []string{
	"RUN_FAILED", "RUN_STUCK", "RUN_LOST", "RUN_TIMEOUT", "RUN_RETRY_EXHAUSTED", "RUN_DUPLICATE_SUSPECTED",
	"QUEUE_BACKLOG", "QUEUE_AGE_HIGH", "QUEUE_NO_CONSUMER", "WORKER_OFFLINE", "WORKER_CAPACITY_HIGH",
	"SCHEDULE_DELAYED", "SCHEDULE_MISSED", "PII_DETECTED", "PII_SCAN_FAILED", "PII_POLICY_VIOLATED", "MONITORING_GAP",
}

func TestEveryRFCAlertTypeHasADataSourceTheCodeProvides(t *testing.T) {
	// alert type -> the annotation type or metric a rule for it reads
	source := map[string]string{
		"RUN_FAILED": "metric:run.failed_age", "RUN_STUCK": "annotation:RUN_STUCK", "RUN_LOST": "annotation:RUN_LOST",
		"RUN_TIMEOUT": "metric:run.timed_out_age", "RUN_RETRY_EXHAUSTED": "metric:run.retries_exhausted_age",
		"RUN_DUPLICATE_SUSPECTED": "annotation:RUN_DUPLICATE_SUSPECTED", "QUEUE_BACKLOG": "metric:queue.pending_count",
		"QUEUE_AGE_HIGH": "metric:queue.oldest_age", "QUEUE_NO_CONSUMER": "metric:queue.consumer_count",
		"WORKER_OFFLINE": "metric:worker.heartbeat_age", "WORKER_CAPACITY_HIGH": "metric:worker.capacity_used",
		"SCHEDULE_DELAYED": "metric:schedule.creation_lateness", "SCHEDULE_MISSED": "annotation:SCHEDULE_MISSED",
		"PII_DETECTED": "metric:pii.policy_action", "PII_SCAN_FAILED": "metric:pii.scan_failed_age",
		"PII_POLICY_VIOLATED": "metric:pii.policy_action", "MONITORING_GAP": "metric:monitoring.status",
	}

	if len(rfcAlertTypes) != 17 {
		t.Fatalf("RFC-007 §4 names 17 alert types, this list has %d", len(rfcAlertTypes))
	}
	for _, alertType := range rfcAlertTypes {
		src, ok := source[alertType]
		if !ok {
			t.Errorf("%s has no data source at all", alertType)
			continue
		}
		if strings.HasPrefix(src, "metric:") && MetricResolvers[strings.TrimPrefix(src, "metric:")] == nil {
			t.Errorf("%s reads metric %q, which has no resolver", alertType, src)
		}
	}
}

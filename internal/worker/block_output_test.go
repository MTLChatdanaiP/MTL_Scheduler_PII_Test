package worker

// RFC-006 §11 / RFC-007 §4: a BLOCK finding in what a run PRODUCED. The run has already happened, so it cannot
// be stopped; the output is stored redacted and the finding is recorded as BLOCK, which is what the existing
// pii.policy_action metric reads to raise PII_POLICY_VIOLATED.

import (
	"context"
	"strings"
	"testing"

	alerts "MTL_Scheduler_PII_Test/internal/alerting"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func TestBlockInOutput_IsStoredRedactedAndRaisesThePolicyViolatedMetric(t *testing.T) {
	p := artifactPolicy()
	p.Spec.Rules = []models.PolicyRule{{ID: "block-phone-output", Priority: 100,
		Match:  models.MatchConditions{Sources: []string{"ERROR_MESSAGE"}, PIITypes: []string{"Phone"}},
		Action: models.PolicyAction{Type: "BLOCK"}}}
	useTestPolicy(t, p)

	// fail_invalid_input echoes the payload into its error output
	task := newRunnableTask(t, "fail_invalid_input", "n", `call 555-123-4567`)
	ProcessTask(context.Background(), task.JobId, "block-output-worker")

	a := bySource(artifactsFor(t, task.JobId), "ERROR_MESSAGE")
	if a == nil {
		t.Fatal("no ERROR_MESSAGE artifact")
	}
	if strings.Contains(a.SanitizedBody, "555-123-4567") {
		t.Fatalf("a BLOCK finding in output must not be stored raw: %q", a.SanitizedBody)
	}
	if !strings.Contains(a.SanitizedBody, "[Phone-1]") {
		t.Fatalf("it is stored redacted: %q", a.SanitizedBody)
	}

	var rec models.PIIRecord
	if err := database.DB.Where("job_id = ? AND source = ?", task.JobId, "ERROR_MESSAGE").First(&rec).Error; err != nil {
		t.Fatalf("the finding must be recorded: %v", err)
	}
	if rec.PolicyAction != "BLOCK" {
		t.Fatalf("policy action = %q, want BLOCK", rec.PolicyAction)
	}

	// and the existing metric now has something to fire PII_POLICY_VIOLATED on
	samples, err := alerts.MetricResolvers["pii.policy_action"](context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range samples {
		if s.Text == "BLOCK" && s.ScopeValue == "Phone" {
			found = true
		}
	}
	if !found {
		t.Fatal("pii.policy_action must report a BLOCK sample for the Phone category: PII_POLICY_VIOLATED is reachable")
	}
}

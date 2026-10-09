package taskservice

// Batch 5 (5A): what happens to a task's payload when a finding cannot be applied. Pure: no database.

import (
	"encoding/json"
	"testing"

	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/pii"
)

func b5Finding(path, match string, start int) pii.EvaluatedFinding {
	return pii.EvaluatedFinding{
		Finding: pii.Finding{Type: "Email", Match: match, DetectorID: "d", FieldPath: path, Start: start, End: start + len(match)},
		Rule:    models.PolicyRule{ID: "r", Action: models.PolicyAction{Type: "REDACT"}},
	}
}

func TestRewritePayloadJSON_AppliesAFindingThatMatches(t *testing.T) {
	task := models.Task{Payload: `["jane@example.com"]`}
	if !rewritePayloadJSON(&task, []pii.EvaluatedFinding{b5Finding("[0]", "jane@example.com", 0)}) {
		t.Fatal("expected success")
	}
	if task.Payload != `["[Email-REDACTED]"]` {
		t.Fatalf("payload = %s", task.Payload)
	}
}

func TestRewritePayloadJSON_WithholdsThePayloadWhenAFindingCannotBeApplied(t *testing.T) {
	task := models.Task{Payload: `{"a":"jane@example.com"}`}

	if rewritePayloadJSON(&task, []pii.EvaluatedFinding{b5Finding("no.such.path", "jane@example.com", 0)}) {
		t.Fatal("a finding that matches no string must fail the rewrite")
	}
	if task.Payload != payloadWithheld {
		t.Fatalf("the raw payload must not be kept: %s", task.Payload)
	}
	if !json.Valid([]byte(task.Payload)) {
		t.Fatal("the placeholder must itself be valid JSON")
	}
}

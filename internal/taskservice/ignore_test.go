package taskservice

// RFC-006 §11 IGNORE through the real CreateTask_Direct. An ignored match leaves NO trace: no finding, no vault entry,
// no event, no transformation.

import (
	"strings"
	"testing"

	"MTL_Scheduler_PII_Test/internal/models"
)

func ignoreRule(id string, priority int, source string, fieldPaths ...string) models.PolicyRule {
	return models.PolicyRule{ID: id, Priority: priority,
		Match:  models.MatchConditions{Sources: []string{source}, PIITypes: []string{"Email"}, FieldPaths: fieldPaths},
		Action: models.PolicyAction{Type: "IGNORE"}}
}

func redactEmail(priority int, source string) models.PolicyRule {
	return models.PolicyRule{ID: "redact-email-" + source, Priority: priority,
		Match:  models.MatchConditions{Sources: []string{source}, PIITypes: []string{"Email"}},
		Action: models.PolicyAction{Type: "REDACT"}}
}

func TestIgnore_AnIgnoredFieldIsStoredUnchangedAndLeavesNoTrace(t *testing.T) {
	useTestPolicy(t, testPolicy(ignoreRule("ignore-qa", 200, "JOB_PAYLOAD", "qa.email"), redactEmail(100, "JOB_PAYLOAD")))

	got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"qa":{"email":"qa@example.com"},"customer":{"email":"jane@example.com"}}`})

	if !strings.Contains(got.Payload, `"email":"qa@example.com"`) {
		t.Fatalf("the ignored value must be stored exactly as given: %s", got.Payload)
	}
	if strings.Contains(got.Payload, "jane@example.com") || !strings.Contains(got.Payload, "[Email-REDACTED]") {
		t.Fatalf("the other email must still follow its REDACT rule: %s", got.Payload)
	}

	recs := recordsFor(t, got.JobId)
	if len(recs) != 1 || recs[0].FieldPath != "customer.email" {
		t.Fatalf("only the NON-ignored finding may be recorded, got %+v", recs)
	}
	if n := vaultRows(t, got.JobId); n != 1 {
		t.Fatalf("only the non-ignored value is vaulted, found %d vault rows", n)
	}
	if n := eventsOf(t, got.JobId, "pii.detected"); n != 1 {
		t.Fatalf("an ignored value must raise no pii.detected event, found %d for two emails", n)
	}
}

func TestIgnore_APayloadWithOnlyIgnoredMatchesIsCLEAN(t *testing.T) {
	useTestPolicy(t, testPolicy(ignoreRule("ignore-qa", 200, "JOB_PAYLOAD", "qa.email"), redactEmail(100, "JOB_PAYLOAD")))

	got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"qa":{"email":"qa@example.com"}}`})

	if got.ScanStatus != "CLEAN" {
		t.Fatalf("scan status = %q: everything the detector found was deliberately ignored, so there are no findings", got.ScanStatus)
	}
	if len(recordsFor(t, got.JobId)) != 0 || vaultRows(t, got.JobId) != 0 || eventsOf(t, got.JobId, "pii.detected") != 0 {
		t.Fatal("an ignored match must leave no record, no vault row and no event")
	}
}

func TestIgnore_ATaskNameCanBeIgnoredToo(t *testing.T) {
	useTestPolicy(t, testPolicy(ignoreRule("ignore-name", 200, "JOB_METADATA"), redactEmail(100, "JOB_METADATA")))

	got := create(t, models.Task{TaskName: "report for qa@example.com", TaskType: "Dummy", Payload: `{}`})

	if got.TaskName != "report for qa@example.com" {
		t.Fatalf("an ignored task name must be left alone, got %q", got.TaskName)
	}
	if len(recordsFor(t, got.JobId)) != 0 || vaultRows(t, got.JobId) != 0 {
		t.Fatal("and leave no trace")
	}
}

func TestIgnore_DoesNotDisableTheDetectorElsewhere(t *testing.T) {
	// "without disabling the detector globally": the same policy still redacts an email in a field it does not name
	useTestPolicy(t, testPolicy(ignoreRule("ignore-qa", 200, "JOB_PAYLOAD", "qa.email"), redactEmail(100, "JOB_PAYLOAD")))

	got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"other":{"email":"x@example.com"}}`})

	if got.ScanStatus != "DETECTED" || strings.Contains(got.Payload, "x@example.com") {
		t.Fatalf("the detector must still work outside the ignored field: status=%q payload=%s", got.ScanStatus, got.Payload)
	}
}

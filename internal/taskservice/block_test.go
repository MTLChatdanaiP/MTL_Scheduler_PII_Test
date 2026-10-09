package taskservice

// RFC-006 §11 BLOCK, RFC-007 §4 PII_POLICY_VIOLATED. A rule that says "a task containing this must not run".

import (
	"strings"
	"testing"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func blockRule(id string, priority int, source string, piiType string) models.PolicyRule {
	return models.PolicyRule{ID: id, Priority: priority,
		Match:  models.MatchConditions{Sources: []string{source}, PIITypes: []string{piiType}},
		Action: models.PolicyAction{Type: "BLOCK"}}
}

func eventsOf(t *testing.T, jobID, eventType string) int64 {
	t.Helper()
	var n int64
	database.DB.Model(&models.EventEnvelope{}).Where("job_id = ? AND event_type = ?", jobID, eventType).Count(&n)
	return n
}

func vaultRows(t *testing.T, jobID string) int64 {
	t.Helper()
	var n int64
	database.DB.Model(&models.PIIVault{}).Where("job_id = ?", jobID).Count(&n)
	return n
}

func TestBlock_APayloadMatchingABlockRuleIsStoredBlockedAndRedacted(t *testing.T) {
	useTestPolicy(t, testPolicy(blockRule("block-phone", 100, "JOB_PAYLOAD", "Phone")))

	got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"p":"555-123-4567","ok":"fine"}`})

	if got.Status != "Blocked" {
		t.Fatalf("status = %q, want Blocked", got.Status)
	}
	var stored models.Task
	database.DB.Where("job_id = ?", got.JobId).First(&stored)
	if stored.Status != "Blocked" {
		t.Fatalf("the STORED status must be Blocked, got %q", stored.Status)
	}
	if strings.Contains(stored.Payload, "555-123-4567") {
		t.Fatalf("a blocked task must not keep the value it was blocked for: %s", stored.Payload)
	}
	if !strings.Contains(stored.Payload, "[Phone-REDACTED]") || !strings.Contains(stored.Payload, `"ok":"fine"`) {
		t.Fatalf("the finding is replaced and everything else is kept: %s", stored.Payload)
	}
	if stored.ScanStatus != "DETECTED" {
		t.Fatalf("scan status = %q, want DETECTED", stored.ScanStatus)
	}
}

func TestBlock_TheFindingIsRecordedAsBLOCKAndTheRawValueIsNeverVaulted(t *testing.T) {
	useTestPolicy(t, testPolicy(blockRule("block-phone", 100, "JOB_PAYLOAD", "Phone")))

	got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"p":"555-123-4567"}`})

	recs := recordsFor(t, got.JobId)
	if len(recs) != 1 || recs[0].PolicyAction != "BLOCK" || recs[0].RuleID != "block-phone" {
		t.Fatalf("expected one finding with action BLOCK from the rule, got %+v", recs)
	}
	// this record is what the existing pii.policy_action metric reads to raise PII_POLICY_VIOLATED
	if recs[0].FingerprintValue == "" || strings.Contains(recs[0].FingerprintValue, "555") {
		t.Fatalf("the finding holds a fingerprint, never the value: %q", recs[0].FingerprintValue)
	}
	if n := vaultRows(t, got.JobId); n != 0 {
		t.Fatalf("a value the policy says must not be processed must not be kept in the vault, found %d rows", n)
	}
}

func TestBlock_EmitsTaskBlockedAndPolicyViolated(t *testing.T) {
	useTestPolicy(t, testPolicy(blockRule("block-phone", 100, "JOB_PAYLOAD", "Phone")))

	got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"p":"555-123-4567"}`})

	for _, et := range []string{"task.created", "task.blocked", "pii.policy_violated"} {
		if n := eventsOf(t, got.JobId, et); n != 1 {
			t.Errorf("expected exactly one %s event, found %d", et, n)
		}
	}
	if n := eventsOf(t, got.JobId, "task.queued"); n != 0 {
		t.Errorf("a blocked task is never queued, found %d task.queued events", n)
	}
}

func TestBlock_ABlockedTaskIsNeverSelectableByTheScheduler(t *testing.T) {
	useTestPolicy(t, testPolicy(blockRule("block-phone", 100, "JOB_PAYLOAD", "Phone")))

	got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"p":"555-123-4567"}`})

	// the scheduler picks up only status = 'Pending' (publishDueTasks), and the recovery sweep only 'Queued'
	var n int64
	database.DB.Model(&models.Task{}).Where("job_id = ? AND (status = ? OR status = ?)", got.JobId, "Pending", "Queued").Count(&n)
	if n != 0 {
		t.Fatal("a Blocked task must be invisible to both the scheduler and the stranded-task sweep, or it would run")
	}
	if got.PublishedAt != nil {
		t.Fatal("and it was never published")
	}
}

func TestBlock_ARuleOnTheTaskNameBlocksToo(t *testing.T) {
	useTestPolicy(t, testPolicy(blockRule("block-email-in-name", 100, "JOB_METADATA", "Email")))

	got := create(t, models.Task{TaskName: "export for jane.doe@example.com", TaskType: "Dummy", Payload: `{"clean":"yes"}`})

	if got.Status != "Blocked" {
		t.Fatalf("a BLOCK rule on JOB_METADATA must block, status = %q", got.Status)
	}
	if strings.Contains(got.TaskName, "jane.doe") {
		t.Fatalf("the blocked task must not keep the email in its name: %q", got.TaskName)
	}
	if n := vaultRows(t, got.JobId); n != 0 {
		t.Fatalf("no vault entry for a blocked finding, found %d", n)
	}
}

func TestBlock_ATextPayloadIsBlockedAndRedactedToo(t *testing.T) {
	useTestPolicy(t, testPolicy(blockRule("block-phone", 100, "JOB_PAYLOAD", "Phone")))

	got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: "please call 555-123-4567 today"})

	if got.Status != "Blocked" || strings.Contains(got.Payload, "555-123-4567") {
		t.Fatalf("the plain-text path must block and redact too: status=%q payload=%q", got.Status, got.Payload)
	}
}

func TestBlock_OtherFindingsStillFollowTheirOwnRules(t *testing.T) {
	useTestPolicy(t, testPolicy(append(standardRules(), blockRule("block-phone", 300, "JOB_PAYLOAD", "Phone"))...))

	got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"p":"555-123-4567","e":"jane.doe@example.com"}`})

	if got.Status != "Blocked" {
		t.Fatalf("status = %q", got.Status)
	}
	if strings.Contains(got.Payload, "jane.doe@example.com") || strings.Contains(got.Payload, "555-123-4567") {
		t.Fatalf("neither value may survive: %s", got.Payload)
	}
	if !strings.Contains(got.Payload, "ja******@example.com") {
		t.Fatalf("the email still follows its own MASK rule: %s", got.Payload)
	}
	// only the BLOCKed value is withheld from the vault; the masked email is vaulted as ever
	if n := vaultRows(t, got.JobId); n != 1 {
		t.Fatalf("expected exactly the email vaulted, found %d vault rows", n)
	}
}

func TestBlock_CleanAndMerelyRedactedTasksAreUnaffected(t *testing.T) {
	t.Run("a clean payload runs normally", func(t *testing.T) {
		useTestPolicy(t, testPolicy(blockRule("block-phone", 100, "JOB_PAYLOAD", "Phone")))
		got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"ok":"fine"}`})
		if got.Status == "Blocked" || got.ScanStatus != "CLEAN" {
			t.Fatalf("status=%q scan=%q", got.Status, got.ScanStatus)
		}
	})
	t.Run("a REDACT rule redacts and does not block", func(t *testing.T) {
		useTestPolicy(t, testPolicy(models.PolicyRule{ID: "redact-phone", Priority: 100,
			Match:  models.MatchConditions{Sources: []string{"JOB_PAYLOAD"}, PIITypes: []string{"Phone"}},
			Action: models.PolicyAction{Type: "REDACT"}}))
		got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"p":"555-123-4567"}`})
		if got.Status == "Blocked" {
			t.Fatal("only a BLOCK rule may block")
		}
	})
}

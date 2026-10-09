package batch5_test

// RFC-000 §5: pii.RecordFinding is the one place a finding record is made. Every field is checked, including the ones that must be EMPTY.

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/pii"
)

func b5Input(action string) pii.FindingInput {
	rule := models.PolicyRule{ID: "rule-1", Action: models.PolicyAction{Type: action}}
	if action == "MASK" {
		rule.Action.Mask = models.MaskConfig{Strategy: "KEEP_SUFFIX"}
	}
	var policy models.PIIPolicy
	policy.Metadata.Name, policy.Metadata.Version, policy.Metadata.Checksum = "default", 7, "abc123"
	return pii.FindingInput{
		JobID: "b5-rf-" + ulid.Make().String(), Source: "JOB_PAYLOAD", Index: 3,
		Finding: pii.Finding{Type: "Email", Match: "jane.doe@example.com", DetectorID: "builtin-email", FieldPath: "customer.email", Start: 4, End: 24},
		Rule:    rule, Policy: policy,
	}
}

func b5SavedRecord(t *testing.T, in pii.FindingInput) models.PIIRecord {
	t.Helper()
	b5DB(t)
	if _, err := pii.RecordFinding(context.Background(), database.DB, in); err != nil {
		t.Fatalf("RecordFinding: %v", err)
	}
	t.Cleanup(func() { database.DB.Unscoped().Where("job_id = ?", in.JobID).Delete(&models.PIIRecord{}) })
	var saved models.PIIRecord
	if err := database.DB.Where("job_id = ?", in.JobID).First(&saved).Error; err != nil {
		t.Fatalf("the record was not saved: %v", err)
	}
	return saved
}

func TestRecordFinding_EveryFieldIsWhatTheCallSitesAlwaysWrote(t *testing.T) {
	in := b5Input("REDACT")
	got := b5SavedRecord(t, in)

	want := map[string]interface{}{
		"JobID": in.JobID, "AttemptID": "", "Type": "Email", "DetectorID": "builtin-email", "Index": 3, "Source": "JOB_PAYLOAD",
		"Confidence": 1.0, "PolicyAction": "REDACT", "FieldPath": "customer.email", "RuleID": "rule-1", "MaskStrategy": "",
		"PolicyName": "default", "PolicyVersion": 7, "PolicyChecksum": "abc123",
	}
	have := map[string]interface{}{
		"JobID": got.JobID, "AttemptID": got.AttemptID, "Type": got.Type, "DetectorID": got.DetectorID, "Index": got.Index, "Source": got.Source,
		"Confidence": got.Confidence, "PolicyAction": got.PolicyAction, "FieldPath": got.FieldPath, "RuleID": got.RuleID, "MaskStrategy": got.MaskStrategy,
		"PolicyName": got.PolicyName, "PolicyVersion": got.PolicyVersion, "PolicyChecksum": got.PolicyChecksum,
	}
	for field, w := range want {
		if have[field] != w {
			t.Errorf("%s = %v, want %v", field, have[field], w)
		}
	}
}

func TestRecordFinding_NeverStoresTheRawValue(t *testing.T) {
	in := b5Input("REDACT")
	got := b5SavedRecord(t, in)

	if got.FingerprintValue == in.Finding.Match || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(got.FingerprintValue) {
		t.Fatalf("the record holds a 64-hex keyed fingerprint, never the value: %q", got.FingerprintValue)
	}
	if got.FingerprintValue != pii.Fingerprint(in.Finding.Match) {
		t.Fatal("the fingerprint must be the keyed fingerprint of the matched text")
	}
	blob, _ := json.Marshal(got)
	if strings.Contains(string(blob), "jane.doe") {
		t.Fatalf("the raw value is in the saved record: %s", blob)
	}
}

func TestRecordFinding_OnlyAMaskRuleSetsAMaskStrategy(t *testing.T) {
	if got := b5SavedRecord(t, b5Input("MASK")); got.MaskStrategy != "KEEP_SUFFIX" || got.PolicyAction != "MASK" {
		t.Fatalf("a MASK finding records its strategy: %+v", got)
	}
	if got := b5SavedRecord(t, b5Input("BLOCK")); got.MaskStrategy != "" {
		t.Fatalf("a BLOCK finding has no mask strategy: %q", got.MaskStrategy)
	}
}

func TestRecordFinding_AnArtifactFindingKeepsItsAttemptID(t *testing.T) {
	in := b5Input("REDACT")
	in.AttemptID, in.Source = "attempt-9", "ERROR_MESSAGE"
	if got := b5SavedRecord(t, in); got.AttemptID != "attempt-9" || got.Source != "ERROR_MESSAGE" {
		t.Fatalf("%+v", got)
	}
}

func TestRecordFinding_ReturnsTheDatabaseError(t *testing.T) {
	b5DB(t)
	failCreatesOn(t, "pii_records")
	if _, err := pii.RecordFinding(context.Background(), database.DB, b5Input("REDACT")); err == nil {
		t.Fatal("the caller decides how to fail, so a failed insert must be returned, not swallowed")
	}
}

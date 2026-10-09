package taskservice

// RFC-006 §8 JOB_METADATA, and guards for CreateTask_Direct's other duties.
//
// This package had no tests. It owns the scan boundary and the idempotency check,
// and both have been silently reverted by an unrelated edit before, so the
// regression guards below are as important as the new behaviour.

import (
	"context"
	"os"
	"testing"

	"github.com/joho/godotenv"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/pii"
)

func TestMain(m *testing.M) {
	godotenv.Load("../../.env")
	database.ConnectDatabase()

	database.DB.AutoMigrate(
		&models.Task{}, &models.ExecutionChain{}, &models.PIIRecord{}, &models.PIIVault{},
		&models.EventEnvelope{}, &models.RunProjection{},
	)

	// The vault needs real keys. Use the configured ones if present, otherwise
	// test-only values, so these tests never depend on the developer's .env.
	if os.Getenv("PII_ENCR_KEY") == "" {
		os.Setenv("PII_ENCR_KEY", "taskservice-test-only-encryption-key")
	}
	if os.Getenv("PII_FINGERPRINT_KEY") == "" {
		os.Setenv("PII_FINGERPRINT_KEY", "taskservice-test-only-fingerprint-key")
	}

	// GetLoadedPolicy dereferences this pointer, so it must never be nil
	pii.LoadedPolicy.Store(&models.PIIPolicy{})

	os.Exit(m.Run())
}

func testPolicy(rules ...models.PolicyRule) models.PIIPolicy {
	var p models.PIIPolicy
	p.Metadata.Name = "taskservice-test"
	p.Metadata.Version = 1
	p.Spec.EvaluationMode = "FIRST_MATCH"
	p.Spec.Defaults.Action = "OBSERVE"
	p.Spec.Detectors = []models.DetectorDefinition{
		{ID: "t-phone", PIIType: "Phone", Type: "REGEX", Enabled: true, Pattern: `\d{3}-\d{3}-\d{4}`, MinimumConfidence: 1.0},
		{ID: "t-email", PIIType: "Email", Type: "REGEX", Enabled: true, Pattern: `[^\s@]+@[^\s@]+\.[^\s@]+`, MinimumConfidence: 1.0},
	}
	p.Spec.Rules = rules
	return p
}

// standardRules: email masked in the payload and, separately, in metadata.
func standardRules() []models.PolicyRule {
	return []models.PolicyRule{
		{ID: "payload-email", Priority: 200,
			Match:  models.MatchConditions{Sources: []string{"JOB_PAYLOAD"}, PIITypes: []string{"Email"}},
			Action: models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "EMAIL", VisibleCharacters: 2, MaskCharacter: "*", DomainMode: "PRESERVE"}}},
		{ID: "metadata-email", Priority: 300,
			Match:  models.MatchConditions{Sources: []string{"JOB_METADATA"}, PIITypes: []string{"Email"}},
			Action: models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "EMAIL", LocalVisiblePrefix: 2, MaskCharacter: "*", DomainMode: "PRESERVE"}}},
	}
}

func useTestPolicy(t *testing.T, p models.PIIPolicy) {
	t.Helper()
	prev := pii.LoadedPolicy.Load()
	pii.LoadedPolicy.Store(&p)
	t.Cleanup(func() { pii.LoadedPolicy.Store(prev) })
}

func create(t *testing.T, task models.Task) models.Task {
	t.Helper()
	got := CreateTask_Direct(context.Background(), task)
	t.Cleanup(func() {
		database.DB.Unscoped().Where("job_id = ?", got.JobId).Delete(&models.Task{})
		database.DB.Unscoped().Where("job_id = ?", got.JobId).Delete(&models.PIIRecord{})
		database.DB.Unscoped().Where("job_id = ?", got.JobId).Delete(&models.PIIVault{})
		database.DB.Unscoped().Where("job_id = ?", got.JobId).Delete(&models.EventEnvelope{})
		database.DB.Unscoped().Where("job_id = ?", got.JobId).Delete(&models.RunProjection{})
		database.DB.Unscoped().Where("execution_chain_id = ?", got.ExecutionChainId).Delete(&models.ExecutionChain{})
	})
	return got
}

func recordsFor(t *testing.T, jobID string) []models.PIIRecord {
	t.Helper()
	var rows []models.PIIRecord
	if err := database.DB.Where("job_id = ?", jobID).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

// ---------------------------------------------------------------------------
// JOB_METADATA
// ---------------------------------------------------------------------------

func TestCreateTask_ScansTheTaskNameAsJobMetadata(t *testing.T) {
	useTestPolicy(t, testPolicy(standardRules()...))

	got := create(t, models.Task{TaskName: "export for jane.doe@example.com", TaskType: "Dummy", Payload: `{"note":"hello"}`})

	if got.TaskName != "export for ja******@example.com" {
		t.Fatalf("returned task name = %q", got.TaskName)
	}

	var stored models.Task
	database.DB.Where("job_id = ?", got.JobId).First(&stored)
	if stored.TaskName != "export for ja******@example.com" {
		t.Fatalf("the STORED task name is what every dashboard shows, got %q", stored.TaskName)
	}

	recs := recordsFor(t, got.JobId)
	if len(recs) != 1 {
		t.Fatalf("expected exactly one finding, got %d: %+v", len(recs), recs)
	}
	r := recs[0]
	if r.Source != "JOB_METADATA" || r.FieldPath != "task_name" || r.Type != "Email" || r.PolicyAction != "MASK" || r.RuleID != "metadata-email" {
		t.Fatalf("finding does not identify its source/field/rule correctly: %+v", r)
	}
	if r.FingerprintValue == "" || r.FingerprintValue == "jane.doe@example.com" {
		t.Fatalf("a finding stores a fingerprint, never the value: %q", r.FingerprintValue)
	}

	// the raw value is vaulted exactly as a payload finding's would be
	var vault []models.PIIVault
	database.DB.Where("job_id = ?", got.JobId).Find(&vault)
	if len(vault) != 1 {
		t.Fatalf("expected the raw value vaulted, got %d vault rows", len(vault))
	}
	raw, err := pii.Decrypt(vault[0].EncryptedValue)
	if err != nil || raw != "jane.doe@example.com" {
		t.Fatalf("vault should hold the raw email, got %q (%v)", raw, err)
	}
}

func TestCreateTask_IdentifierFieldsAreNeverRewritten(t *testing.T) {
	useTestPolicy(t, testPolicy(standardRules()...))

	// TaskType picks the handler. If a rule could rewrite it, a REDACT would silently
	// change which code runs, so only descriptive text is scanned.
	got := create(t, models.Task{TaskName: "plain name", TaskType: "weird@type.example", Payload: `{}`})

	if got.TaskType != "weird@type.example" {
		t.Fatalf("TaskType was rewritten to %q", got.TaskType)
	}
	if len(recordsFor(t, got.JobId)) != 0 {
		t.Fatal("no metadata finding should exist for a clean task name")
	}
}

func TestCreateTask_MetadataFindingIndexContinuesAfterThePayloads(t *testing.T) {
	useTestPolicy(t, testPolicy(standardRules()...))

	got := create(t, models.Task{TaskName: "for z.w@example.com", TaskType: "Dummy", Payload: `{"contact":"x.y@example.com"}`})

	recs := recordsFor(t, got.JobId)
	if len(recs) != 2 {
		t.Fatalf("expected a payload finding and a metadata finding, got %d", len(recs))
	}

	idx := map[string]int{}
	for _, r := range recs {
		idx[r.Source] = r.Index
	}
	// two Email findings on one job must not both be "Email 1": the operator would
	// not be able to tell which value the vault entry belongs to
	if idx["JOB_PAYLOAD"] != 1 || idx["JOB_METADATA"] != 2 {
		t.Fatalf("expected payload index 1 and metadata index 2, got %v", idx)
	}
}

func TestCreateTask_ScanStatusCombinesBothPasses(t *testing.T) {
	useTestPolicy(t, testPolicy(standardRules()...))

	tests := []struct {
		name string
		task models.Task
		want string
	}{
		{"both clean", models.Task{TaskName: "plain", TaskType: "Dummy", Payload: `{"a":"b"}`}, "CLEAN"},
		{"only the name has PII", models.Task{TaskName: "for a.b@example.com", TaskType: "Dummy", Payload: `{"a":"b"}`}, "DETECTED"},
		{"only the payload has PII", models.Task{TaskName: "plain", TaskType: "Dummy", Payload: `{"a":"c.d@example.com"}`}, "DETECTED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := create(t, tt.task); got.ScanStatus != tt.want {
				t.Fatalf("ScanStatus = %q, want %q", got.ScanStatus, tt.want)
			}
		})
	}
}

func TestCreateTask_AnEmptyTaskNameIsNotScanned(t *testing.T) {
	useTestPolicy(t, testPolicy(standardRules()...))

	got := create(t, models.Task{TaskName: "", TaskType: "Dummy", Payload: `{}`})

	if got.TaskName != "" || len(recordsFor(t, got.JobId)) != 0 {
		t.Fatalf("nothing to scan, yet name=%q and %d findings", got.TaskName, len(recordsFor(t, got.JobId)))
	}
}

func TestMergeScanStatus(t *testing.T) {
	tests := []struct{ a, b, want string }{
		{"CLEAN", "CLEAN", "CLEAN"},
		{"CLEAN", "DETECTED", "DETECTED"},
		{"DETECTED", "CLEAN", "DETECTED"},
		{"DETECTED", "SCAN_ERROR", "SCAN_ERROR"},
		{"SCAN_ERROR", "DETECTED", "SCAN_ERROR"},
		{"CLEAN", "", "CLEAN"}, // that pass did not run
		{"", "DETECTED", "DETECTED"},
	}
	for _, tt := range tests {
		if got := mergeScanStatus(tt.a, tt.b); got != tt.want {
			t.Errorf("mergeScanStatus(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// The queue a rule sees. task.Queue is assigned the real stream name, and it used
// to be assigned AFTER the scan, so a queues-scoped rule saw the caller's value
// (usually empty) and could never match.
// ---------------------------------------------------------------------------

func TestCreateTask_AQueueScopedRuleSeesTheRealQueue(t *testing.T) {
	queueRule := func(queue string) models.PolicyRule {
		return models.PolicyRule{ID: "q-" + queue, Priority: 100,
			Match:  models.MatchConditions{Sources: []string{"JOB_PAYLOAD"}, Queues: []string{queue}, PIITypes: []string{"Phone"}},
			Action: models.PolicyAction{Type: "REDACT"}}
	}

	t.Run("a rule for the real queue matches although the caller sent none", func(t *testing.T) {
		useTestPolicy(t, testPolicy(queueRule("tasks:stream")))
		got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"p":"555-123-4567"}`})
		// the JSON path writes [Type-REDACTED], plain text writes [Type-n]
		if got.Payload != `{"p":"[Phone-REDACTED]"}` {
			t.Fatalf("a queue-scoped rule for tasks:stream did not apply, payload = %s", got.Payload)
		}
	})

	t.Run("a rule for a different queue does not match", func(t *testing.T) {
		useTestPolicy(t, testPolicy(queueRule("some-other-queue")))
		got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{"p":"555-123-4567"}`})
		if got.Payload != `{"p":"555-123-4567"}` {
			t.Fatalf("a rule scoped to another queue must not redact, payload = %s", got.Payload)
		}
	})
}

// ---------------------------------------------------------------------------
// Regression guard: an unrelated edit to this file once deleted the idempotency check.
// ---------------------------------------------------------------------------

func TestCreateTask_ARepeatedIdempotencyKeyReturnsTheOriginalTask(t *testing.T) {
	useTestPolicy(t, testPolicy())

	key := "idem-" + t.Name()
	first := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{}`, IdempotencyKey: &key})
	second := CreateTask_Direct(context.Background(), models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{}`, IdempotencyKey: &key})

	if second.JobId != first.JobId {
		t.Fatalf("the same idempotency key produced two tasks: %s and %s", first.JobId, second.JobId)
	}

	var n int64
	database.DB.Model(&models.Task{}).Where("idempotency_key = ?", key).Count(&n)
	if n != 1 {
		t.Fatalf("expected exactly one stored task for the key, got %d", n)
	}
}

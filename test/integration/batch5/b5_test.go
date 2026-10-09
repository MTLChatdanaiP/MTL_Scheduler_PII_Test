package batch5_test

// Batch 5 (5A) through the real CreateTask_Direct and a real database.
//
// Self-contained on purpose: it has no TestMain and no helper from another test file, so it works wherever the other
// integration tests live. It connects (and migrates) once, the first time a test asks for the database.

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/joho/godotenv"
	"gorm.io/gorm"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/pii"
	"MTL_Scheduler_PII_Test/internal/taskservice"
)

func init() {
	if os.Getenv("PII_FINGERPRINT_KEY") == "" {
		os.Setenv("PII_FINGERPRINT_KEY", "0123456789abcdef0123456789abcdef")
	}
	if os.Getenv("PII_ENCR_KEY") == "" {
		os.Setenv("PII_ENCR_KEY", "fedcba9876543210fedcba9876543210")
	}
}

var dbOnce sync.Once

func b5DB(t *testing.T) {
	t.Helper()
	dbOnce.Do(func() {
		godotenv.Load("../../../.env")
		if database.DB == nil {
			database.ConnectDatabase()
		}
		if err := database.DB.AutoMigrate(&models.Task{}, &models.ExecutionChain{}, &models.PIIRecord{}, &models.PIIVault{}, &models.EventEnvelope{},
			&models.RunProjection{}, &models.Attempt{}, &models.AuditRecord{}, &models.MonitoringAnnotation{}, &models.Alert{},
			&models.ScheduleOccurrence{}, &models.ScheduleProjection{}, &models.ScheduleDefinition{}); err != nil {
			panic("batch5 tests: could not migrate: " + err.Error())
		}
	})
}

// b5Policy: Email and Phone are both REDACTed, whatever the source, so the payload and the task name use the same rules.
func b5Policy(t *testing.T) {
	t.Helper()
	var p models.PIIPolicy
	p.Metadata.Name, p.Metadata.Version, p.Metadata.Checksum = "b5", 1, "b5-checksum"
	p.Spec.EvaluationMode = "FIRST_MATCH"
	p.Spec.Defaults.Action = "OBSERVE"
	p.Spec.Detectors = []models.DetectorDefinition{
		{ID: "b5-email", PIIType: "Email", Type: "REGEX", Enabled: true, Pattern: `[^\s@"]+@[^\s@"]+\.[^\s@"]+`, MinimumConfidence: 1},
		{ID: "b5-phone", PIIType: "Phone", Type: "REGEX", Enabled: true, Pattern: `\b\d{3}-\d{3}-\d{4}\b`, MinimumConfidence: 1},
	}
	p.Spec.Rules = []models.PolicyRule{
		{ID: "b5-email-rule", Priority: 200, Match: models.MatchConditions{PIITypes: []string{"Email"}}, Action: models.PolicyAction{Type: "REDACT"}},
		{ID: "b5-phone-rule", Priority: 100, Match: models.MatchConditions{PIITypes: []string{"Phone"}}, Action: models.PolicyAction{Type: "REDACT"}},
	}
	prev := pii.LoadedPolicy.Load()
	pii.LoadedPolicy.Store(&p)
	t.Cleanup(func() { pii.LoadedPolicy.Store(prev) })
}

func b5Create(t *testing.T, name, payload string) models.Task {
	t.Helper()
	b5DB(t)
	b5Policy(t)
	got := taskservice.CreateTask_Direct(context.Background(), models.Task{TaskName: name, TaskType: "Dummy", Payload: payload})
	t.Cleanup(func() {
		database.DB.Unscoped().Where("job_id = ?", got.JobId).Delete(&models.Task{})
		database.DB.Unscoped().Where("job_id = ?", got.JobId).Delete(&models.PIIRecord{})
		database.DB.Unscoped().Where("job_id = ?", got.JobId).Delete(&models.PIIVault{})
		database.DB.Unscoped().Where("job_id = ?", got.JobId).Delete(&models.EventEnvelope{})
		database.DB.Unscoped().Where("job_id = ?", got.JobId).Delete(&models.RunProjection{})
	})
	return got
}

func b5Stored(t *testing.T, jobID string) models.Task {
	t.Helper()
	var stored models.Task
	if err := database.DB.Where("job_id = ?", jobID).First(&stored).Error; err != nil {
		t.Fatalf("task %s is not in the database: %v", jobID, err)
	}
	return stored
}

func b5Count(model interface{}, where string, args ...interface{}) int64 {
	var n int64
	database.DB.Model(model).Where(where, args...).Count(&n)
	return n
}

// ------------------------------------------------------------------------------------------------- S1: any payload shape

func TestCreateTask_ARootArrayPayloadDoesNotKeepTheRawValue(t *testing.T) {
	// reproduced before the fix: DETECTED, finding recorded, value vaulted, and the raw value still in the stored payload
	got := b5Create(t, "n", `[{"p":"555-123-4567"}]`)
	stored := b5Stored(t, got.JobId)

	if strings.Contains(stored.Payload, "555-123-4567") {
		t.Fatalf("the raw value is in the STORED payload: %s", stored.Payload)
	}
	if stored.Payload != `[{"p":"[Phone-REDACTED]"}]` || stored.ScanStatus != "DETECTED" || stored.Status == "Blocked" {
		t.Fatalf("payload=%s scan=%s status=%s", stored.Payload, stored.ScanStatus, stored.Status)
	}
	if b5Count(&models.PIIRecord{}, "job_id = ?", got.JobId) != 1 || b5Count(&models.PIIVault{}, "job_id = ?", got.JobId) != 1 {
		t.Fatal("the finding must still be recorded and the original value vaulted")
	}
}

func TestCreateTask_RootStringAndNestedArrayPayloads(t *testing.T) {
	for payload, want := range map[string]string{
		`"mail jane@example.com now"`:                `"mail [Email-REDACTED] now"`,
		`{"a":[["x@y.org"],[{"b":"555-123-4567"}]]}`: `{"a":[["[Email-REDACTED]"],[{"b":"[Phone-REDACTED]"}]]}`,
	} {
		got := b5Stored(t, b5Create(t, "n", payload).JobId)
		if got.Payload != want {
			t.Errorf("payload %s\n got  %s\n want %s", payload, got.Payload, want)
		}
	}
}

func TestCreateTask_AKeyThatLooksLikeANestedPathCannotHideAValue(t *testing.T) {
	got := b5Stored(t, b5Create(t, "n", `{"a.b":"x@y.com","a":{"b":"p@q.com"}}`).JobId)
	if strings.Contains(got.Payload, "@") {
		t.Fatalf("a raw value survived: %s", got.Payload)
	}
}

func TestCreateTask_ACleanPayloadIsStoredByteForByte(t *testing.T) {
	payload := `{"b":1,  "a":[ 2 , "fine" ]}`
	if got := b5Stored(t, b5Create(t, "n", payload).JobId); got.Payload != payload || got.ScanStatus != "CLEAN" {
		t.Fatalf("a payload with nothing to redact must not be reformatted: %q (%s)", got.Payload, got.ScanStatus)
	}
}

func TestCreateTask_NumbersInAPayloadSurviveARewrite(t *testing.T) {
	got := b5Stored(t, b5Create(t, "n", `{"id":12345678901234567890,"e":"jane@example.com"}`).JobId)
	if !strings.Contains(got.Payload, `"id":12345678901234567890`) {
		t.Fatalf("a large integer must not be rewritten as a float: %s", got.Payload)
	}
}

// ------------------------------------------------------------------------------------------------- S6: NFC

func TestCreateTask_NonNFCTextIsRedactedOnTheValue(t *testing.T) {
	// "e" + a combining accent is one byte longer than "é"; the redaction used to land a byte early and leave a character of the value behind
	got := b5Stored(t, b5Create(t, "cafe\u0301 jane.doe@example.com", `{"n":"cafe\u0301 contact jane.doe@example.com now"}`).JobId)

	if got.TaskName != "caf\u00e9 [Email-REDACTED]" {
		t.Errorf("task name: %q", got.TaskName)
	}
	if got.Payload != "{\"n\":\"caf\u00e9 contact [Email-REDACTED] now\"}" {
		t.Errorf("payload: %q", got.Payload)
	}

	plain := b5Stored(t, b5Create(t, "n", "cafe\u0301 contact jane.doe@example.com now").JobId) // not JSON: the plain-text path
	if strings.Contains(plain.Payload, "jane") || strings.Contains(plain.Payload, "com") || !strings.HasPrefix(plain.Payload, "caf\u00e9 contact [Email-") {
		t.Errorf("plain-text payload: %q", plain.Payload)
	}
}

// ------------------------------------------------------------------------------------------------- S5: evidence failures

func failCreatesOn(t *testing.T, table string) {
	t.Helper()
	name := "b5:fail:" + table
	database.DB.Callback().Create().Before("gorm:create").Register(name, func(db *gorm.DB) {
		if db.Statement.Table == table {
			db.AddError(errors.New("blocked by the test"))
		}
	})
	t.Cleanup(func() { database.DB.Callback().Create().Remove(name) })
}

func TestCreateTask_AFindingThatCannotBeRecordedBlocksTheTask(t *testing.T) {
	b5DB(t)
	failCreatesOn(t, "pii_records")

	got := b5Create(t, "n", `{"e":"jane@example.com"}`)
	stored := b5Stored(t, got.JobId)

	if stored.Status != "Blocked" || stored.ScanStatus != "SCAN_ERROR" {
		t.Fatalf("status=%s scan=%s: a task whose evidence could not be saved must never run", stored.Status, stored.ScanStatus)
	}
	if strings.Contains(stored.Payload, "jane@example.com") {
		t.Fatalf("the payload must still be redacted: %s", stored.Payload)
	}
	if b5Count(&models.EventEnvelope{}, "job_id = ? AND event_type = ?", got.JobId, "pii.evidence_write_failed") == 0 {
		t.Fatal("expected a pii.evidence_write_failed event")
	}
}

func TestCreateTask_AVaultEntryThatCannotBeSavedBlocksTheTask(t *testing.T) {
	b5DB(t)
	failCreatesOn(t, "pii_vaults")

	stored := b5Stored(t, b5Create(t, "n", `{"e":"jane@example.com"}`).JobId)
	if stored.Status != "Blocked" || stored.ScanStatus != "SCAN_ERROR" {
		t.Fatalf("status=%s scan=%s", stored.Status, stored.ScanStatus)
	}
}

func TestCreateTask_ATaskNameFindingThatCannotBeRecordedBlocksTheTask(t *testing.T) {
	b5DB(t)
	failCreatesOn(t, "pii_records")

	stored := b5Stored(t, b5Create(t, "report for jane@example.com", `{"ok":"fine"}`).JobId)
	if stored.Status != "Blocked" || stored.ScanStatus != "SCAN_ERROR" || strings.Contains(stored.TaskName, "jane@") {
		t.Fatalf("status=%s scan=%s name=%q", stored.Status, stored.ScanStatus, stored.TaskName)
	}
}

func TestCreateTask_WhenNothingFailsNothingIsBlocked(t *testing.T) {
	got := b5Stored(t, b5Create(t, "n", `{"e":"jane@example.com"}`).JobId)
	if got.Status == "Blocked" || got.ScanStatus != "DETECTED" {
		t.Fatalf("the normal path must be unchanged: status=%s scan=%s", got.Status, got.ScanStatus)
	}
}

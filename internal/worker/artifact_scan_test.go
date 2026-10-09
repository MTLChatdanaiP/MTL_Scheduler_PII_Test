package worker

// RFC-006 §24 / §8: post-execution scanning, exercised the way production reaches
// it -- through ProcessTask, not by calling the scanner directly. The earlier
// tests called ScanAndPersistArtifact directly, which is why they could not see
// that the permanent-failure path never called it at all.

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/pii"
)

func artifactPolicy() models.PIIPolicy {
	var p models.PIIPolicy
	p.Metadata.Name = "artifact-test"
	p.Metadata.Version = 1
	p.Spec.EvaluationMode = "FIRST_MATCH"
	p.Spec.Defaults.Action = "OBSERVE"
	p.Spec.Detectors = []models.DetectorDefinition{
		{ID: "t-phone", PIIType: "Phone", Type: "REGEX", Enabled: true, Pattern: `\d{3}-\d{3}-\d{4}`, MinimumConfidence: 1.0},
		{ID: "t-email", PIIType: "Email", Type: "REGEX", Enabled: true, Pattern: `[^\s@]+@[^\s@]+\.[^\s@]+`, MinimumConfidence: 1.0},
		{ID: "t-card", PIIType: "CreditCard", Type: "REGEX", Enabled: true, Pattern: `\d{4}-\d{4}-\d{4}-\d{4}`, MinimumConfidence: 1.0},
	}
	sources := []string{"JOB_RESULT", "ERROR_MESSAGE", "STRUCTURED_LOG"}
	p.Spec.Rules = []models.PolicyRule{
		{ID: "r-phone", Priority: 100, Match: models.MatchConditions{Sources: sources, PIITypes: []string{"Phone"}}, Action: models.PolicyAction{Type: "REDACT"}},
		{ID: "r-card", Priority: 200, Match: models.MatchConditions{Sources: sources, PIITypes: []string{"CreditCard"}}, Action: models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "FULL", MaskCharacter: "*"}}},
		{ID: "r-email", Priority: 300, Match: models.MatchConditions{Sources: sources, PIITypes: []string{"Email"}}, Action: models.PolicyAction{Type: "MASK", Mask: models.MaskConfig{Strategy: "EMAIL", VisibleCharacters: 2, MaskCharacter: "*", DomainMode: "PRESERVE"}}},
	}
	return p
}

// useTestPolicy installs a policy for one test and restores the previous one after,
// so these tests never depend on what policies/default.json happens to contain.
func useTestPolicy(t *testing.T, p models.PIIPolicy) {
	t.Helper()
	prev := pii.LoadedPolicy.Load()
	pii.LoadedPolicy.Store(&p)
	t.Cleanup(func() { pii.LoadedPolicy.Store(prev) })
}

func artifactsFor(t *testing.T, jobID string) []models.ExecutionArtifact {
	t.Helper()
	var rows []models.ExecutionArtifact
	if err := database.DB.Where("job_id = ?", jobID).Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func bySource(rows []models.ExecutionArtifact, source string) *models.ExecutionArtifact {
	for i := range rows {
		if rows[i].Source == source {
			return &rows[i]
		}
	}
	return nil
}

func cleanupJob(t *testing.T, jobID string) {
	t.Helper()
	t.Cleanup(func() {
		database.DB.Unscoped().Where("job_id = ?", jobID).Delete(&models.ExecutionArtifact{})
		database.DB.Unscoped().Where("job_id = ?", jobID).Delete(&models.PIIRecord{})
		database.DB.Unscoped().Where("job_id = ?", jobID).Delete(&models.Attempt{})
		database.DB.Unscoped().Where("job_id = ?", jobID).Delete(&models.EventEnvelope{})
		database.DB.Unscoped().Where("job_id = ?", jobID).Delete(&models.RunProjection{})
		database.DB.Unscoped().Where("parent_run_id = ?", jobID).Delete(&models.Task{})
		database.DB.Unscoped().Where("job_id = ?", jobID).Delete(&models.Task{})
	})
}

func newRunnableTask(t *testing.T, taskType, taskName, payload string) models.Task {
	t.Helper()
	task := models.Task{
		JobId: ulid.Make().String(), TaskName: taskName, TaskType: taskType, Payload: payload,
		Status: "Pending", Queue: "tasks:stream", RunAt: time.Now().UTC(),
	}
	task.ExecutionChainId = task.JobId
	if err := database.DB.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	cleanupJob(t, task.JobId)
	return task
}

func fastProgress(t *testing.T) {
	t.Helper()
	prev := progressChunkDuration
	progressChunkDuration = 2 * time.Millisecond
	t.Cleanup(func() { progressChunkDuration = prev })
}

// ---------------------------------------------------------------------------
// Through ProcessTask
// ---------------------------------------------------------------------------

func TestProcessTask_EveryOutcomeProducesAScannedArtifact(t *testing.T) {
	useTestPolicy(t, artifactPolicy())
	fastProgress(t)

	tests := []struct {
		taskType string
		source   string
	}{
		{"Dummy", "JOB_RESULT"},
		{"fail_retryable", "ERROR_MESSAGE"},
		// These five all end in NonRetryableFailure. Their scan call used to sit
		// inside the RetryableFailure case behind `if outcome == NonRetryableFailure`,
		// which can never be true there, so none of them was ever scanned or stored.
		{"fail_permanent", "ERROR_MESSAGE"},
		{"fail_invalid_input", "ERROR_MESSAGE"},
		{"fail_dependency", "ERROR_MESSAGE"},
		{"fail_timeout", "ERROR_MESSAGE"},
		{"fail_infrastructure", "ERROR_MESSAGE"},
	}

	for _, tt := range tests {
		t.Run(tt.taskType, func(t *testing.T) {
			task := newRunnableTask(t, tt.taskType, "plain name", `{"note":"hello"}`)

			ProcessTask(context.Background(), task.JobId, "artifact-test-worker")

			rows := artifactsFor(t, task.JobId)
			if bySource(rows, tt.source) == nil {
				t.Fatalf("%s produced no %s artifact (sources stored: %v)", tt.taskType, tt.source, sourcesOf(rows))
			}
			if bySource(rows, "STRUCTURED_LOG") == nil {
				t.Fatalf("%s produced no STRUCTURED_LOG artifact (sources stored: %v)", tt.taskType, sourcesOf(rows))
			}
		})
	}
}

func sourcesOf(rows []models.ExecutionArtifact) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Source)
	}
	return out
}

func TestProcessTask_PermanentFailureArtifactIsSanitizedEndToEnd(t *testing.T) {
	useTestPolicy(t, artifactPolicy())

	// fail_invalid_input echoes the payload into its error output
	task := newRunnableTask(t, "fail_invalid_input", "name", `call 555-123-4567 about 4111-1111-1111-1111`)

	ProcessTask(context.Background(), task.JobId, "artifact-test-worker")

	a := bySource(artifactsFor(t, task.JobId), "ERROR_MESSAGE")
	if a == nil {
		t.Fatal("no ERROR_MESSAGE artifact for a permanent failure")
	}
	if strings.Contains(a.SanitizedBody, "555-123-4567") || strings.Contains(a.SanitizedBody, "4111") {
		t.Fatalf("raw PII reached the stored artifact: %q", a.SanitizedBody)
	}
	if !strings.Contains(a.SanitizedBody, "[Phone-1]") || !strings.Contains(a.SanitizedBody, "*******************") {
		t.Fatalf("expected the phone redacted and the card fully masked, got %q", a.SanitizedBody)
	}
	if a.ScanStatus != "DETECTED" || a.FindingCount != 2 {
		t.Fatalf("scan status %s with %d findings, want DETECTED with 2", a.ScanStatus, a.FindingCount)
	}
	if a.AttemptID == "" {
		t.Fatal("a post-execution artifact must carry its AttemptID")
	}
}

func TestProcessTask_StructuredLogIsScannedAndSanitized(t *testing.T) {
	useTestPolicy(t, artifactPolicy())

	// the fixture handler's first log line includes the task name
	task := newRunnableTask(t, "fail_permanent", "export for jane.doe@example.com", `{}`)

	ProcessTask(context.Background(), task.JobId, "artifact-test-worker")

	logArtifact := bySource(artifactsFor(t, task.JobId), "STRUCTURED_LOG")
	if logArtifact == nil {
		t.Fatal("no STRUCTURED_LOG artifact")
	}
	if strings.Contains(logArtifact.SanitizedBody, "jane.doe@example.com") {
		t.Fatalf("raw email survived in the stored log: %q", logArtifact.SanitizedBody)
	}
	if !strings.Contains(logArtifact.SanitizedBody, "ja******@example.com") {
		t.Fatalf("expected the email masked in the log, got %q", logArtifact.SanitizedBody)
	}
}

// ---------------------------------------------------------------------------
// Handler contract
// ---------------------------------------------------------------------------

func TestRunHandlerFull_ReturnsLogsAndRunHandlerStillReturnsThreeValues(t *testing.T) {
	task := models.Task{TaskType: "fail_permanent", TaskName: "n"}

	full := runHandlerFull(context.Background(), task)
	if full.Outcome != NonRetryableFailure || len(full.Logs) < 2 {
		t.Fatalf("expected a failure with at least 2 log lines, got %+v", full)
	}

	outcome, category, output := runHandler(context.Background(), task)
	if outcome != full.Outcome || category != full.Category || output != full.Output {
		t.Fatalf("runHandler must return exactly the first three fields of runHandlerFull")
	}
}

func TestJoinHandlerLogs(t *testing.T) {
	if got := joinHandlerLogs(nil); got != "" {
		t.Fatalf("no lines should give no text, got %q", got)
	}

	lines := make([]string, 25)
	for i := range lines {
		lines[i] = "line"
	}
	got := strings.Split(joinHandlerLogs(lines), "\n")

	// 20 kept lines, then one note saying how many were dropped
	if len(got) != MaxLogLines+1 {
		t.Fatalf("expected %d kept lines plus 1 note, got %d entries", MaxLogLines, len(got))
	}
	for i := 0; i < MaxLogLines; i++ {
		if got[i] != "line" {
			t.Fatalf("entry %d should be a kept line, got %q", i, got[i])
		}
	}
	if got[MaxLogLines] != "[5 more log lines omitted]" {
		t.Fatalf("the dropped lines must be counted, got %q", got[MaxLogLines])
	}
}

// ---------------------------------------------------------------------------
// The scanner itself: it must never crash the worker or store text it could not check
// ---------------------------------------------------------------------------

func TestScanAndPersistArtifact_APanicIsContainedAndTheBodyWithheld(t *testing.T) {
	useTestPolicy(t, artifactPolicy())
	scanStageHook = func() { panic("simulated crash inside the scan stage") }
	t.Cleanup(func() { scanStageHook = nil })

	jobID := ulid.Make().String()
	cleanupJob(t, jobID)

	art, err := ScanAndPersistArtifact(context.Background(), jobID, "att-1", "ERROR_MESSAGE", "call 555-123-4567", "T", "q")
	if err != nil {
		t.Fatalf("a scan panic must not become an error for the caller: %v", err)
	}
	if art.ScanStatus != "SCAN_ERROR" || !art.Withheld || art.SanitizedBody != "" {
		t.Fatalf("expected SCAN_ERROR + withheld + empty body, got %s withheld=%v body=%q", art.ScanStatus, art.Withheld, art.SanitizedBody)
	}

	stored := artifactsFor(t, jobID)
	if len(stored) != 1 || !stored[0].Withheld {
		t.Fatalf("the withheld artifact should be recorded, got %+v", stored)
	}
}

func TestScanAndPersistArtifact_AFailedDetectorWithholdsTheBodyInsteadOfStoringItPartlyChecked(t *testing.T) {
	p := artifactPolicy()
	// an enabled detector whose pattern cannot compile; ValidatePolicy would reject
	// it at load, so it is installed directly to simulate a detector failing at run time
	p.Spec.Detectors = append(p.Spec.Detectors, models.DetectorDefinition{ID: "t-broken", PIIType: "Broken", Type: "REGEX", Enabled: true, Pattern: `(`, MinimumConfidence: 1.0})
	useTestPolicy(t, p)

	jobID := ulid.Make().String()
	cleanupJob(t, jobID)

	art, err := ScanAndPersistArtifact(context.Background(), jobID, "att-1", "ERROR_MESSAGE", "call 555-123-4567 now", "T", "q")
	if err != nil {
		t.Fatal(err)
	}
	if art.ScanStatus != "SCAN_ERROR" || !art.Withheld || art.SanitizedBody != "" {
		t.Fatalf("a partly-failed scan must withhold the body, got %s withheld=%v body=%q", art.ScanStatus, art.Withheld, art.SanitizedBody)
	}

	// the finding from the detectors that DID work is still recorded: it holds a
	// fingerprint and metadata, never the raw value
	var n int64
	database.DB.Model(&models.PIIRecord{}).Where("job_id = ?", jobID).Count(&n)
	if n != 1 {
		t.Fatalf("expected the working detector's finding to be recorded, got %d", n)
	}
}

func TestScanAndPersistArtifact_ThaiTextIsCutOnACharacterBoundary(t *testing.T) {
	useTestPolicy(t, artifactPolicy())

	// each of these characters is 3 bytes in UTF-8, and 8192 is not a multiple of 3,
	// so a plain byte slice at the cap lands in the middle of a character
	body := strings.Repeat("สวัสดี", 3000)

	jobID := ulid.Make().String()
	cleanupJob(t, jobID)

	art, err := ScanAndPersistArtifact(context.Background(), jobID, "att-1", "STRUCTURED_LOG", body, "T", "q")
	if err != nil {
		t.Fatalf("a long Thai artifact must persist (Postgres rejects invalid UTF-8): %v", err)
	}
	if !art.Truncated || len(art.SanitizedBody) > MaxArtifactBytes {
		t.Fatalf("expected a truncated body within %d bytes, got %d bytes truncated=%v", MaxArtifactBytes, len(art.SanitizedBody), art.Truncated)
	}
	if !utf8.ValidString(art.SanitizedBody) {
		t.Fatal("the stored body was cut through the middle of a character")
	}
	if art.OriginalBytes != len(body) {
		t.Fatalf("OriginalBytes = %d, want %d", art.OriginalBytes, len(body))
	}
}

func TestScanAndPersistArtifact_InvalidUTF8AndNULBytesStillPersist(t *testing.T) {
	useTestPolicy(t, artifactPolicy())

	jobID := ulid.Make().String()
	cleanupJob(t, jobID)

	art, err := ScanAndPersistArtifact(context.Background(), jobID, "att-1", "ERROR_MESSAGE", "bad \xff\xfe bytes and a nul \x00 here", "T", "q")
	if err != nil {
		t.Fatalf("untrusted handler output must not make the insert fail: %v", err)
	}
	if !utf8.ValidString(art.SanitizedBody) || strings.ContainsRune(art.SanitizedBody, 0) {
		t.Fatalf("stored body must be valid UTF-8 without NULs, got %q", art.SanitizedBody)
	}
}

func TestTruncateOnRuneBoundary(t *testing.T) {
	if got, cut := truncateOnRuneBoundary("short", 100); got != "short" || cut {
		t.Fatalf("under the limit nothing should change, got %q cut=%v", got, cut)
	}
	if got, cut := truncateOnRuneBoundary("abcdef", 3); got != "abc" || !cut {
		t.Fatalf("ascii cut wrong: %q cut=%v", got, cut)
	}
	// "สวัสดี" is 18 bytes; a 10-byte limit lands inside the 4th character
	got, cut := truncateOnRuneBoundary("สวัสดี", 10)
	if !cut || !utf8.ValidString(got) || len(got) > 10 {
		t.Fatalf("Thai cut must stay valid and within the limit, got %q (%d bytes)", got, len(got))
	}
}

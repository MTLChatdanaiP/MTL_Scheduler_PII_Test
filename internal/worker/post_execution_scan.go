package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/events"
	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/pii"
)

// RFC-006 §24 Post-Execution Scanning.
//
//	worker output/error/log -> PII engine -> policy action -> safe persistence
//
// Each stage below is one step of that sequence. The ordering is the whole
// point of the section: nothing is written anywhere until after the policy has
// been applied, so a raw artifact body exists only as a local variable and is
// never handed to monitoring, the run lifecycle, or any log sink.

// MaxArtifactBytes bounds what is kept per artifact. The RFC asks for "bounded
// persistence"; without a cap this table would quietly become the general log
// sink §24 exists to prevent. Truncation is recorded rather than silent, so an
// investigator can tell a short artifact from a trimmed one.
const MaxArtifactBytes = 8 * 1024

// MaxLogLines bounds how many of a handler's log lines are scanned per attempt.
const MaxLogLines = 20

// scanStageHook is a test-only injection point, run at the start of the scan
// stage, so a test can force a panic and prove the worker survives it. It is nil
// in production.
var scanStageHook func()

// scanResult is everything the scan stage learned, computed entirely in memory.
// Nothing here has touched the database.
type scanResult struct {
	evaluated []pii.EvaluatedFinding
	indices   []int // the per-type index each finding is stored and numbered with
	sanitized string
	status    string // CLEAN, DETECTED or SCAN_ERROR
}

// cleanArtifactText takes a handler's raw output and returns text Postgres will
// accept, by replacing invalid UTF-8 and removing NUL bytes. A handler's output
// is untrusted, and either of these makes an INSERT fail, which would silently
// lose the artifact.
func cleanArtifactText(body string) string {
	body = strings.ToValidUTF8(body, "\uFFFD")
	return strings.ReplaceAll(body, "\x00", "")
}

// truncateOnRuneBoundary takes text and a byte limit and returns the text cut to
// at most that many bytes without splitting a multi-byte character, plus whether
// anything was cut. A plain s[:n] can slice through the middle of a character
// (Thai text is 3 bytes a character), producing invalid UTF-8 that Postgres
// rejects.
func truncateOnRuneBoundary(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}

	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// scanSafely takes an artifact body and its scan context and returns the scan
// result, by detecting PII, resolving overlaps and applying the policy's actions
// in memory, all under recover().
//
// A panic anywhere in that stage is converted into status SCAN_ERROR instead of
// crashing the worker goroutine (and with it the whole process). RFC-006 §2: a
// scan failing is not the job failing.
func scanSafely(body string, source string, jobType string, queue string, policy models.PIIPolicy) (res scanResult) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic while scanning an artifact, it will be withheld", "source", source, "panic", fmt.Sprint(r))
			res = scanResult{status: "SCAN_ERROR"}
		}
	}()

	if scanStageHook != nil {
		scanStageHook()
	}

	// Same two-path approach the pre-execution scanner uses: structured JSON when
	// the body parses as JSON (so field paths are real), plain text otherwise. A
	// worker's error message is usually not JSON, so the fallback is the common
	// case here rather than the exception.
	evaluated, failedDetectors, structured := pii.DetectJSON(body, policy.Spec.Detectors, policy, source, jobType, queue)
	if !structured {
		findings, failed := pii.Detect(body, policy.Spec.Detectors)
		evaluated = pii.EvaluatePolicy(findings, policy, source, jobType, queue)
		failedDetectors = failed
	}
	evaluated = pii.ResolveOverlaps(evaluated)
	evaluated = pii.DropIgnored(evaluated) // RFC-006 §11 IGNORE

	perType := make(map[pii.PIIType]int)
	indices := make([]int, len(evaluated))
	var transformations []pii.Transformation

	for i, ev := range evaluated {
		finding, rule := ev.Finding, ev.Rule
		perType[finding.Type]++
		indices[i] = perType[finding.Type]

		switch rule.Action.Type {
		// the run has already happened, so BLOCK cannot stop it; the output is stored redacted and the
		// finding is recorded with action BLOCK, which is what raises PII_POLICY_VIOLATED
		case "REDACT", "BLOCK":
			replacement := "[" + string(finding.Type) + "-" + strconv.Itoa(perType[finding.Type]) + "]"
			transformations = append(transformations, pii.Transformation{Start: finding.Start, End: finding.End, Replacement: replacement})
		case "MASK":
			transformations = append(transformations, pii.Transformation{Start: finding.Start, End: finding.End, Replacement: pii.Mask(finding.Match, rule.Action.Mask)})
		}
	}

	sanitized := body
	if structured {
		out, err := pii.ApplyFindingsToJSONChecked(body, evaluated)
		if err != nil {
			// RFC-006 fail closed: findings exist that could not be applied to the output. SCAN_ERROR makes the caller
			// WITHHOLD the body; the findings are still returned so their records are kept.
			return scanResult{evaluated: evaluated, indices: indices, status: "SCAN_ERROR"}
		}
		sanitized = out
	} else if len(transformations) > 0 {
		sort.Slice(transformations, func(i, j int) bool { return transformations[i].Start > transformations[j].Start })
		// positions came from the NFC form of the text, so they are applied to the NFC form
		sanitized = pii.ApplyTransformations(pii.NormalizeForScan(body), transformations)
	}

	status := "CLEAN"
	switch {
	case len(failedDetectors) > 0:
		status = "SCAN_ERROR"
	case len(evaluated) > 0:
		status = "DETECTED"
	}

	return scanResult{evaluated: evaluated, indices: indices, sanitized: sanitized, status: status}
}

// ScanAndPersistArtifact takes one artifact a worker produced (its result, its
// error message, its log lines) and returns the stored row.
//
// It detects PII in the body, applies the active policy's action to every
// finding, persists one PIIRecord per finding (carrying the AttemptID, which a
// pre-execution scan cannot have), and stores ONLY the transformed text.
//
// If the scan cannot be trusted (a detector failed, or the scan crashed) the body
// is WITHHELD: FAIL_OPEN means "don't block the job", not "store text we could
// not fully check in the monitoring database".
//
// The raw body is never persisted and never published. jobType and queue are
// passed to the match engine so a rule scoped by jobTypes or queues applies here
// exactly as it does before execution.
func ScanAndPersistArtifact(ctx context.Context, jobID string, attemptID string, source string, body string, jobType string, queue string) (models.ExecutionArtifact, error) {
	policy := pii.GetLoadedPolicy()

	originalBytes := len(body)
	body = cleanArtifactText(body)

	artifact := models.ExecutionArtifact{
		JobID:          jobID,
		AttemptID:      attemptID,
		Source:         source,
		OriginalBytes:  originalBytes,
		PolicyName:     policy.Metadata.Name,
		PolicyVersion:  policy.Metadata.Version,
		PolicyChecksum: policy.Metadata.Checksum,
		ProducedAt:     time.Now().UTC(),
	}

	if body == "" {
		artifact.ScanStatus = "CLEAN"
		err := database.DB.WithContext(ctx).Create(&artifact).Error
		return artifact, err
	}

	// ---- stages 2 and 3: PII engine + policy action, in memory ---------------
	res := scanSafely(body, source, jobType, queue, policy)
	artifact.ScanStatus = res.status
	artifact.FindingCount = len(res.evaluated)

	// A finding holds a fingerprint and metadata, never the raw value, so it is
	// safe to record even when the body itself has to be withheld.
	for i, ev := range res.evaluated {
		finding, rule := ev.Finding, ev.Rule

		if _, err := pii.RecordFinding(ctx, database.DB, pii.FindingInput{
			JobID:     jobID,
			AttemptID: attemptID, // the field RFC-006 §17 defines but pre-execution scans can never fill
			Source:    source,
			Index:     res.indices[i],
			Finding:   finding,
			Rule:      rule,
			Policy:    policy,
		}); err != nil {
			fmt.Println("failed to persist post-execution PII finding:", err)
		}
		events.LogEvent(ctx, jobID, "pii.detected", "worker")
	}

	// ---- stage 4: safe, bounded persistence ---------------------------------
	if res.status == "SCAN_ERROR" {
		artifact.Withheld = true
		events.LogEvent(ctx, jobID, "pii.scan_failed", "worker")
	} else {
		// Truncation happens AFTER transformation, never before: trimming first
		// could cut a value in half and leave a partial unredacted fragment, and
		// it cuts on a character boundary so the stored text stays valid UTF-8.
		artifact.SanitizedBody, artifact.Truncated = truncateOnRuneBoundary(res.sanitized, MaxArtifactBytes)
	}

	if err := database.DB.WithContext(ctx).Create(&artifact).Error; err != nil {
		return artifact, err
	}

	if !artifact.Withheld {
		events.LogEvent(ctx, jobID, "pii.artifact_scanned", "worker")
	}
	return artifact, nil
}

// joinHandlerLogs takes a handler's log lines and returns one block of text to
// scan, by keeping the first MaxLogLines lines and noting how many were dropped.
func joinHandlerLogs(lines []string) string {
	if len(lines) == 0 {
		return ""
	}

	shown := lines
	omitted := 0
	if len(lines) > MaxLogLines {
		shown = lines[:MaxLogLines]
		omitted = len(lines) - MaxLogLines
	}

	text := strings.Join(shown, "\n")
	if omitted > 0 {
		text += fmt.Sprintf("\n[%d more log lines omitted]", omitted)
	}
	return text
}

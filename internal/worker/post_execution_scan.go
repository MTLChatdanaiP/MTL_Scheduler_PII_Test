package worker

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

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

// ScanAndPersistArtifact takes one artifact a worker produced (its result, its
// error message, a log line) and returns the stored row.
//
// It detects PII in the body, applies the active policy's action to every
// finding, persists one PIIRecord per finding (carrying the AttemptID, which a
// pre-execution scan cannot have), and stores ONLY the transformed text.
//
// The raw body is never persisted and never published: callers pass it in, it
// is transformed in memory, and the caller's copy goes out of scope with the
// stack frame.
func ScanAndPersistArtifact(ctx context.Context, jobID string, attemptID string, source string, body string) (models.ExecutionArtifact, error) {
	policy := pii.GetLoadedPolicy()

	artifact := models.ExecutionArtifact{
		JobID:          jobID,
		AttemptID:      attemptID,
		Source:         source,
		OriginalBytes:  len(body),
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

	// ---- stage 2: PII engine -------------------------------------------------
	// Same two-path approach the pre-execution scanner uses: structured JSON
	// when the body parses as JSON (so field paths are real), plain text
	// otherwise. A worker's error message is usually not JSON, so the fallback
	// is the common case here rather than the exception.
	evaluated, failedDetectors, structured := pii.DetectJSON(body, policy.Spec.Detectors, policy, source, "")
	if !structured {
		findings, failed := pii.Detect(body, policy.Spec.Detectors)
		evaluated = pii.EvaluatePolicy(findings, policy, source, "")
		failedDetectors = failed
	}
	evaluated = pii.ResolveOverlaps(evaluated)

	switch {
	case len(failedDetectors) > 0:
		// RFC-006 §2: a scan failing is NOT the job failing. This is recorded
		// on the artifact, never on the task's own status.
		artifact.ScanStatus = "SCAN_ERROR"
		events.LogEvent(ctx, jobID, "pii.scan_failed", "worker")
	case len(evaluated) == 0:
		artifact.ScanStatus = "CLEAN"
	default:
		artifact.ScanStatus = "DETECTED"
	}
	artifact.FindingCount = len(evaluated)

	// ---- stage 3: policy action ---------------------------------------------
	perType := make(map[pii.PIIType]int)
	var transformations []pii.Transformation

	for _, ev := range evaluated {
		finding, rule := ev.Finding, ev.Rule
		perType[finding.Type]++

		record := models.PIIRecord{
			JobID:            jobID,
			AttemptID:        attemptID, // the field §17 defines but pre-execution scans can never fill
			Type:             string(finding.Type),
			DetectorID:       finding.DetectorID,
			FingerprintValue: pii.Fingerprint(finding.Match),
			Index:            perType[finding.Type],
			Source:           source,
			Confidence:       1.0,
			PolicyAction:     rule.Action.Type,
			FieldPath:        finding.FieldPath,
			RuleID:           rule.ID,
			PolicyName:       policy.Metadata.Name,
			PolicyVersion:    policy.Metadata.Version,
			PolicyChecksum:   policy.Metadata.Checksum,
		}
		if rule.Action.Type == "MASK" {
			record.MaskStrategy = rule.Action.Mask.Strategy
		}
		if err := database.DB.WithContext(ctx).Create(&record).Error; err != nil {
			fmt.Println("failed to persist post-execution PII finding:", err)
		}
		events.LogEvent(ctx, jobID, "pii.detected", "worker")

		switch rule.Action.Type {
		case "REDACT":
			replacement := "[" + string(finding.Type) + "-" + strconv.Itoa(perType[finding.Type]) + "]"
			transformations = append(transformations, pii.Transformation{Start: finding.Start, End: finding.End, Replacement: replacement})
		case "MASK":
			transformations = append(transformations, pii.Transformation{Start: finding.Start, End: finding.End, Replacement: pii.Mask(finding.Match, rule.Action.Mask)})
		}
	}

	sanitized := body
	if structured {
		sanitized = pii.ApplyFindingsToJSON(body, evaluated)
	} else {
		// Applied back-to-front so each replacement cannot shift the offsets of
		// the ones not yet applied.
		sort.Slice(transformations, func(i, j int) bool { return transformations[i].Start > transformations[j].Start })
		sanitized = pii.ApplyTransformations(body, transformations)
	}

	// ---- stage 4: safe, bounded persistence ---------------------------------
	// Truncation happens AFTER transformation, never before: trimming first
	// could cut a value in half and leave a partial unredacted fragment behind.
	if len(sanitized) > MaxArtifactBytes {
		sanitized = sanitized[:MaxArtifactBytes]
		artifact.Truncated = true
	}
	artifact.SanitizedBody = sanitized

	if err := database.DB.WithContext(ctx).Create(&artifact).Error; err != nil {
		return artifact, err
	}

	events.LogEvent(ctx, jobID, "pii.artifact_scanned", "worker")
	return artifact, nil
}

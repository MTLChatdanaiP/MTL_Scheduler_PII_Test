package taskservice

import (
	"context"
	"log/slog"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/events"
	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/pii"
)

// RFC-006 §8 JOB_METADATA.
//
// Caller-supplied descriptive text on a job is scanned like the payload. TaskName
// is free text that appears in every run list, alert and event, so PII placed in it
// leaks across the whole dashboard even when the payload is clean.
//
// TaskType, Queue, ScheduleId and the chain ids are deliberately NOT scanned. They
// are identifiers used for routing and rule matching; redacting TaskType would
// silently change which handler runs.

const (
	metadataTaskNameKey = "task_name"
	metadataSource      = "JOB_METADATA"
	taskNameWithheld    = "[task name withheld]"
)

// scanRank orders scan statuses by severity so two passes can be combined.
var scanRank = map[string]int{"": 0, "CLEAN": 1, "DETECTED": 2, "SCAN_ERROR": 3}

// mergeScanStatus takes the statuses of two scan passes and returns the more
// severe, by comparing them in the order CLEAN < DETECTED < SCAN_ERROR. An empty
// status means that pass did not run.
func mergeScanStatus(a string, b string) string {
	if scanRank[b] > scanRank[a] {
		return b
	}
	return a
}

// scanJobMetadata takes the task being created, the active policy and the per-type finding counters shared with the
// payload scan, and returns the metadata scan status ("" if there was nothing to scan), whether a BLOCK rule hit, and
// whether any REDACT, MASK or BLOCK was applied.
//
// A task name is always a plain string, so it is scanned as plain text with Detect (no JSON wrapper). Each finding is
// still given the field path "task_name" and matched under source JOB_METADATA, so the policy's rules (including ones
// with fieldPaths) and the stored findings are exactly what they were when the name went through the JSON path.
// REDACT and BLOCK write [Type-REDACTED]; MASK uses pii.Mask. The sanitized name is written back to the task, each
// finding is persisted, and each raw value is vaulted (BLOCK is not). The counters are shared so a metadata finding's
// index continues after the payload's instead of colliding with it.
//
// It cannot crash task creation: any panic withholds the name instead.
func scanJobMetadata(ctx context.Context, task *models.Task, policy models.PIIPolicy, counters map[pii.PIIType]int) (status string, blocked bool, applied bool) {
	if task.TaskName == "" {
		return "", false, false
	}

	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic while scanning job metadata, the task name will be withheld", "panic", r)
			task.TaskName = taskNameWithheld
			status = "SCAN_ERROR"
		}
	}()

	findings, failedDetectors := pii.Detect(task.TaskName, policy.Spec.Detectors)

	evaluated := make([]pii.EvaluatedFinding, 0, len(findings))
	for _, f := range findings {
		f.FieldPath = metadataTaskNameKey
		rule := pii.ResolveRule(pii.MatchContext{
			Source:     metadataSource,
			JobType:    task.TaskType,
			Queue:      task.Queue,
			PIIType:    string(f.Type),
			DetectorID: f.DetectorID,
			FieldPath:  metadataTaskNameKey,
			Confidence: 1.0,
		}, policy)
		evaluated = append(evaluated, pii.EvaluatedFinding{Finding: f, Rule: rule})
	}
	evaluated = pii.ResolveOverlaps(evaluated)
	evaluated = pii.DropIgnored(evaluated) // RFC-006 §11 IGNORE

	status = "CLEAN"
	switch {
	case len(failedDetectors) > 0:
		status = "SCAN_ERROR"
	case len(evaluated) > 0:
		status = "DETECTED"
	}

	var transformations []pii.Transformation
	evidenceFailed := false // RFC-006 fail closed: a record or vault row could not be saved

	for _, ev := range evaluated {
		finding, rule := ev.Finding, ev.Rule
		counters[finding.Type]++

		if _, err := pii.RecordFinding(ctx, database.DB, pii.FindingInput{
			JobID:   task.JobId,
			Source:  metadataSource,
			Index:   counters[finding.Type],
			Finding: finding,
			Rule:    rule,
			Policy:  policy,
		}); err != nil {
			evidenceFailed = true
		}
		events.LogEventWith(ctx, task.JobId, "pii.detected", "api", events.EventContext{ExecutionChainID: task.ExecutionChainId, TraceID: task.TraceID})

		// what replaces the matched text. BLOCK is stored redacted, like REDACT; OBSERVE changes nothing.
		switch rule.Action.Type {
		case "REDACT", "BLOCK":
			applied = true
			transformations = append(transformations, pii.Transformation{Start: finding.Start, End: finding.End, Replacement: "[" + string(finding.Type) + "-REDACTED]"})
		case "MASK":
			applied = true
			transformations = append(transformations, pii.Transformation{Start: finding.Start, End: finding.End, Replacement: pii.Mask(finding.Match, rule.Action.Mask)})
		}

		// RFC-006 §11 BLOCK: the task will not run, and a value that must not be processed is also not kept
		if rule.Action.Type == "BLOCK" {
			blocked = true
			continue
		}

		encrypted, err := pii.Encrypt(finding.Match)
		if err != nil {
			events.LogEvent(ctx, task.JobId, "pii.encryption.failed", "api")
			continue // no vault entry rather than a fake placeholder, same as the payload path
		}
		if err := database.DB.WithContext(ctx).Create(&models.PIIVault{JobId: task.JobId, Type: string(finding.Type), Index: counters[finding.Type], EncryptedValue: encrypted}).Error; err != nil {
			evidenceFailed = true
		}
	}

	// ApplyTransformations splices from the end of the text backwards, so earlier positions stay correct
	if len(transformations) > 0 {
		// positions came from the NFC form of the name, so they are applied to the NFC form
		task.TaskName = pii.ApplyTransformations(pii.NormalizeForScan(task.TaskName), transformations)
	}

	if evidenceFailed {
		events.LogEventWith(ctx, task.JobId, "pii.evidence_write_failed", "api", events.EventContext{ExecutionChainID: task.ExecutionChainId, TraceID: task.TraceID})
		return "SCAN_ERROR", true, applied
	}

	return status, blocked, applied
}

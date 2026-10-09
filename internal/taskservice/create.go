package taskservice

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"context"

	"github.com/oklog/ulid/v2"
	"gorm.io/gorm/clause"

	"MTL_Scheduler_PII_Test/internal/cache"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/events"
	"MTL_Scheduler_PII_Test/internal/models"
	pii "MTL_Scheduler_PII_Test/internal/pii"
)

func CreateTask_Direct(ctx context.Context, task models.Task) models.Task {

	// RFC-001 §2 Goals: tolerate duplicate command signals -- if this exact
	// caller-supplied key was already used, return the task that was created the
	// first time instead of creating a second one. Checked first, before the new
	// JobId is even generated, so a retried request is genuinely free of side
	// effects beyond the first call.
	if task.IdempotencyKey != nil && *task.IdempotencyKey != "" {
		var existing models.Task
		if err := database.DB.WithContext(ctx).Where("idempotency_key = ?", *task.IdempotencyKey).First(&existing).Error; err == nil {
			fmt.Println("duplicate command signal -- idempotency key already used, returning the original task:", existing.JobId)
			return existing
		}
	}

	// PRD §9 Job Identity Requirements: stable, sortable identifier generated at creation time, before the row is persisted
	task.JobId = ulid.Make().String()
	task.ExecutionChainId = task.JobId

	// RFC-003 §5: every task carries a trace id. A caller-supplied one is kept only if it
	// is a safe id (it travels into Redis and into logs, so it must not be a way to smuggle
	// free text, and therefore PII, there); anything else is replaced.
	if !cache.ValidTraceID(task.TraceID) {
		task.TraceID = cache.NewTraceID()
	}

	// RFC-003 §1: PublishedAt is the scheduler's record that Redis confirmed the publish.
	// Task binds from the request body, so a caller could otherwise send one and make the
	// recovery sweep believe an unpublished task was already published, which would lose it.
	task.PublishedAt = nil

	var exeChain = models.ExecutionChain{
		ExecutionChainId: task.ExecutionChainId,
	}

	err := database.DB.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "execution_chain_id"},
			},
			DoNothing: true,
		}).
		Create(&exeChain).Error

	if err != nil {
		fmt.Println("FAILED TO UPSERT EXECUTION CHAIN: ", err)
	}

	// The queue this task will actually be published to. It must be set BEFORE the
	// scan: it used to be assigned afterwards, so the "queues" match dimension saw
	// whatever the caller happened to send (usually nothing) and a queue-scoped rule
	// could never match a payload.
	task.Queue = cache.TaskStream

	temp := make(map[pii.PIIType]int)

	policy := pii.GetLoadedPolicy()

	// RFC-006 §32: the pre-execution scan has boundaries. The task row does not exist yet, so the run's chain and trace ids are
	// passed explicitly rather than read from it.
	scanCtx := events.EventContext{ExecutionChainID: task.ExecutionChainId, TraceID: task.TraceID}
	events.LogEventWith(ctx, task.JobId, "pii.scan_started", "api", scanCtx)

	evaluatedFindings, failedDetectors, ok := pii.DetectJSON(task.Payload, policy.Spec.Detectors, policy, "JOB_PAYLOAD", task.TaskType, task.Queue)

	if !ok {
		findings, failed := pii.Detect(task.Payload, policy.Spec.Detectors)
		evaluatedFindings = pii.EvaluatePolicy(findings, policy, "JOB_PAYLOAD", task.TaskType, task.Queue)
		failedDetectors = failed
	}

	evaluatedFindings = pii.ResolveOverlaps(evaluatedFindings)

	// RFC-006 §11 IGNORE: after overlap resolution, so an IGNORE rule wins its text like any other rule, then it is dropped
	evaluatedFindings = pii.DropIgnored(evaluatedFindings)

	if len(failedDetectors) > 0 {
		task.ScanStatus = "SCAN_ERROR"
	} else if len(evaluatedFindings) == 0 {
		task.ScanStatus = "CLEAN"
	} else {
		task.ScanStatus = "DETECTED"
	}

	var transformations []pii.Transformation

	// RFC-006 §11 BLOCK: set when any finding's rule says the task must not run
	blocked := false

	// RFC-006 fail closed: set when a finding record or vault row could not be saved. The payload is rewritten anyway, so the
	// original value would be unrecoverable and unrecorded; such a task is never run.
	evidenceFailed := false

	// RFC-006 §32 pii.policy_applied: set when any REDACT, MASK or BLOCK was applied
	applied := false

	// RFC-006 §12 Pre-Execution Scanning: "an implementation may scan... before publishing to Redis... The chosen boundary affects whether raw PII enters Redis." This project scans and redacts before the task is ever saved or published, so raw PII never enters Postgres or the Redis stream
	for _, evaluated_finding := range evaluatedFindings {

		value := evaluated_finding.Finding
		rule := evaluated_finding.Rule

		temp[value.Type] += 1

		// RFC-006 §7 Scan Model — Policy Evaluation stage, REDACT branch: finding is persisted separately (Claim Check) before the payload is rewritten
		if _, err := pii.RecordFinding(ctx, database.DB, pii.FindingInput{
			JobID:   task.JobId,
			Source:  "JOB_PAYLOAD",
			Index:   temp[value.Type],
			Finding: value,
			Rule:    rule,
			Policy:  policy,
		}); err != nil {
			evidenceFailed = true
			fmt.Println("FAILED TO SAVE A PII FINDING RECORD for job", task.JobId)
		}
		events.LogEventWith(ctx, task.JobId, "pii.detected", "api", scanCtx)

		// RFC-006 §14 PII-Safe Logging: payload is rewritten so no downstream system (Redis, worker logs, monitoring) ever sees the raw value
		switch rule.Action.Type {
		case "REDACT":
			replacement := "[" + string(value.Type) + "-" + strconv.Itoa(temp[value.Type]) + "]"
			transformations = append(transformations, pii.Transformation{Start: value.Start, End: value.End, Replacement: replacement})
		case "MASK":
			maskedValue := pii.Mask(value.Match, rule.Action.Mask)
			transformations = append(transformations, pii.Transformation{Start: value.Start, End: value.End, Replacement: maskedValue})
		case "BLOCK":
			// stored redacted, like REDACT, and the task will not run
			blocked = true
			replacement := "[" + string(value.Type) + "-" + strconv.Itoa(temp[value.Type]) + "]"
			transformations = append(transformations, pii.Transformation{Start: value.Start, End: value.End, Replacement: replacement})
		}

		switch rule.Action.Type {
		case "REDACT", "MASK", "BLOCK":
			applied = true
		}

		// a value the policy says must not be processed is also not kept in the vault
		if rule.Action.Type == "BLOCK" {
			continue
		}

		encryptedMatch, err := pii.Encrypt(value.Match)
		if err != nil {
			events.LogEvent(ctx, task.JobId, "pii.encryption.failed", "api")
			continue // skip creating a vault entry for this one finding — don't store a fake "ERROR!!!" placeholder as if it were real data
		}

		vault := models.PIIVault{JobId: task.JobId, Type: string(value.Type), Index: temp[value.Type], EncryptedValue: encryptedMatch}
		if err := database.DB.WithContext(ctx).Create(&vault).Error; err != nil {
			evidenceFailed = true
			fmt.Println("FAILED TO SAVE A PII VAULT ENTRY for job", task.JobId)
		}
	}

	if evidenceFailed {
		task.ScanStatus = "SCAN_ERROR"
		blocked = true
		events.LogEventWith(ctx, task.JobId, "pii.evidence_write_failed", "api", scanCtx)
	}

	if ok {
		// RFC-006: the rewrite is CHECKED. If any finding cannot be applied the payload is withheld and the task blocked,
		// instead of being stored with the raw value still in it.
		if !rewritePayloadJSON(&task, evaluatedFindings) {
			task.ScanStatus = "SCAN_ERROR"
			blocked = true
			events.LogEventWith(ctx, task.JobId, "pii.payload_withheld", "api", scanCtx)
		}
	} else if len(transformations) > 0 {
		sort.Slice(transformations, func(i, j int) bool {
			return transformations[i].Start >
				transformations[j].Start
		})
		// positions came from the NFC form of the text, so they are applied to the NFC form
		task.Payload = pii.ApplyTransformations(pii.NormalizeForScan(task.Payload), transformations)
	}

	// RFC-006 §8 JOB_METADATA: scan the caller-supplied task name too. The counters
	// are shared so its finding indices continue after the payload's.
	metaStatus, metaBlocked, metaApplied := scanJobMetadata(ctx, &task, policy, temp)
	task.ScanStatus = mergeScanStatus(task.ScanStatus, metaStatus)
	blocked = blocked || metaBlocked
	applied = applied || metaApplied

	// RFC-006 §11 BLOCK: a Blocked task is stored for the record and never queued, so no Redis message and no worker
	if blocked {
		task.Status = "Blocked"
	}

	events.LogEventWith(ctx, task.JobId, "pii.scan_completed", "api", scanCtx)

	fmt.Println("Final: ", task.Payload)

	// RFC-002 §12 Timezone Requirements / §4 Domain Model (expected_at): normalizes an unset or past-due RunAt to "now", meaning immediate tasks flow through the same scheduler poll loop as scheduled ones (RFC-002 §7 Scheduling Flow) rather than publishing directly here
	if task.RunAt.Before(time.Now().UTC()) {
		task.RunAt = time.Now().UTC()
	}

	// RFC-000 §5.3 Domain Events Are Facts: run.created-equivalent event
	database.DB.WithContext(ctx).Create(&task)
	if task.SourceRunId == "" {
		events.LogEvent(ctx, task.JobId, "task.created", "api")
	} else {
		events.LogEvent(ctx, task.JobId, "task.rerun_created", "api")
	}

	if applied {
		events.LogEvent(ctx, task.JobId, "pii.policy_applied", "api")
	}

	if blocked {
		events.LogEvent(ctx, task.JobId, "task.blocked", "api")
		events.LogEvent(ctx, task.JobId, "pii.policy_violated", "api")
	}
	return task
}

// payloadWithheld replaces a payload that could not be rewritten safely. It is itself valid JSON, so anything that parses
// task payloads still can.
const payloadWithheld = `"[payload withheld]"`

// rewritePayloadJSON takes the task and the resolved findings of its JSON payload and replaces task.Payload with the sanitized
// payload. It returns false when the payload could not be rewritten safely; task.Payload is then payloadWithheld, so the raw
// value is not kept anywhere on the task.
func rewritePayloadJSON(task *models.Task, evaluated []pii.EvaluatedFinding) bool {
	sanitized, err := pii.ApplyFindingsToJSONChecked(task.Payload, evaluated)
	if err != nil {
		task.Payload = payloadWithheld
		return false
	}
	task.Payload = sanitized
	return true
}

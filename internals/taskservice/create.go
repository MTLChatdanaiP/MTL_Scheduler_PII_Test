package taskservice

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"context"

	"github.com/oklog/ulid/v2"
	"gorm.io/gorm/clause"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/events"
	"MTL_Scheduler_PII_Test/internals/models"
	pii "MTL_Scheduler_PII_Test/internals/pii"
)

func CreateTask_Direct(ctx context.Context, task models.Task) models.Task {

	// PRD §9 Job Identity Requirements: stable, sortable identifier generated at creation time, before the row is persisted
	task.JobId = ulid.Make().String()
	task.ExecutionChainId = task.JobId

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

	temp := make(map[pii.PIIType]int)

	policy := pii.GetLoadedPolicy()

	evaluatedFindings, failedDetectors, ok := pii.DetectJSON(task.Payload, policy.Spec.Detectors, policy, "JOB_PAYLOAD", task.TaskType)

	if !ok {
		findings, failed := pii.Detect(task.Payload, policy.Spec.Detectors)
		evaluatedFindings = pii.EvaluatePolicy(findings, policy, "JOB_PAYLOAD", task.TaskType)
		failedDetectors = failed
	}

	evaluatedFindings = pii.ResolveOverlaps(evaluatedFindings)

	if len(failedDetectors) > 0 {
		task.ScanStatus = "SCAN_ERROR"
	} else if len(evaluatedFindings) == 0 {
		task.ScanStatus = "CLEAN"
	} else {
		task.ScanStatus = "DETECTED"
	}

	var transformations []pii.Transformation

	// RFC-006 §12 Pre-Execution Scanning: "an implementation may scan... before publishing to Redis... The chosen boundary affects whether raw PII enters Redis." This project scans and redacts before the task is ever saved or published, so raw PII never enters Postgres or the Redis stream
	for _, evaluated_finding := range evaluatedFindings {

		value := evaluated_finding.Finding
		rule := evaluated_finding.Rule

		temp[value.Type] += 1

		// RFC-006 §7 Scan Model — Policy Evaluation stage, REDACT branch: finding is persisted separately (Claim Check) before the payload is rewritten
		record := models.PIIRecord{JobID: task.JobId, Type: string(value.Type), DetectorID: value.DetectorID, FingerprintValue: pii.Fingerprint(value.Match), Index: temp[value.Type], Source: "JOB_PAYLOAD", Confidence: 1.0, PolicyAction: rule.Action.Type}
		database.DB.WithContext(ctx).Create(&record)
		events.LogEvent(ctx, task.JobId, "pii.detected", "api")

		// RFC-006 §14 PII-Safe Logging: payload is rewritten so no downstream system (Redis, worker logs, monitoring) ever sees the raw value
		switch rule.Action.Type {
		case "REDACT":
			replacement := "[" + string(value.Type) + "-" + strconv.Itoa(temp[value.Type]) + "]"
			transformations = append(transformations, pii.Transformation{Start: value.Start, End: value.End, Replacement: replacement})
		case "MASK":
			maskedValue := pii.Mask(value.Match, rule.Action.Mask)
			transformations = append(transformations, pii.Transformation{Start: value.Start, End: value.End, Replacement: maskedValue})
		}

		encryptedMatch, err := pii.Encrypt(value.Match)
		if err != nil {
			events.LogEvent(ctx, task.JobId, "pii.encryption.failed", "api")
			continue // skip creating a vault entry for this one finding — don't store a fake "ERROR!!!" placeholder as if it were real data
		}

		vault := models.PIIVault{JobId: task.JobId, Type: string(value.Type), Index: temp[value.Type], EncryptedValue: encryptedMatch}
		database.DB.WithContext(ctx).Create(&vault)
	}

	if ok {
		task.Payload = pii.ApplyFindingsToJSON(
			task.Payload,
			evaluatedFindings,
		)
	} else {
		sort.Slice(transformations, func(i, j int) bool {
			return transformations[i].Start >
				transformations[j].Start
		})
		task.Payload = pii.ApplyTransformations(task.Payload, transformations)
	}

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
	return task
}

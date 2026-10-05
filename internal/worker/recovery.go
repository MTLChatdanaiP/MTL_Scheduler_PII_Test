package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/events"
	"MTL_Scheduler_PII_Test/internal/models"
)

// prepareRecovery runs before the reclaimer reprocesses a message. It returns false when the message
// must be left pending (the task's owner still looks alive, or the facts could not be read), true when
// it is safe to reprocess.
//
// Before this existed a reclaimed task whose worker had died was never run again: ProcessTask saw the
// dead worker's unfinished attempt, took it for a duplicate delivery and returned, and ProcessStream
// then acknowledged the message anyway.
//
// RFC-001 §6: when ownership is lost, the old attempt becomes ABANDONED and transport recovery creates
// another attempt for the SAME run. It is abandoned BEFORE the new attempt is claimed, so the run never
// has two unfinished attempts (RUN_DUPLICATE_SUSPECTED would otherwise fire).
func prepareRecovery(ctx context.Context, jobId string) bool {
	var task models.Task
	if err := database.DB.WithContext(ctx).Where("job_id = ?", jobId).First(&task).Error; err != nil {
		return true // ProcessTask reports a missing task itself
	}

	var unfinished []models.Attempt
	if err := database.DB.WithContext(ctx).
		Where("job_id = ? AND status IN ?", jobId, []string{"Claimed", "Started"}).
		Find(&unfinished).Error; err != nil {
		slog.Error("recovery: could not read attempts, leaving the message pending", "job_id", jobId, "error", err)
		return false
	}

	gone := make([]bool, len(unfinished))
	for i, attempt := range unfinished {
		gone[i] = attemptOwnerGone(ctx, attempt)
	}

	plan := models.PlanRecovery(task.Status, gone)
	if plan.Wait {
		return false
	}

	if plan.Abandon {
		for i := range unfinished {
			slog.Warn("recovery: abandoning an attempt whose worker is gone",
				"job_id", jobId, "attempt_id", unfinished[i].AttemptId, "worker_id", unfinished[i].WorkerId)
			events.MarkAttemptAbandonedFor(ctx, &unfinished[i], models.FailureWorkerFailure)
		}
	}

	return true
}

// attemptOwnerGone collects the evidence about the worker that claimed an attempt. If the evidence
// cannot be read it assumes the worker is alive: running the work twice is worse than waiting.
func attemptOwnerGone(ctx context.Context, attempt models.Attempt) bool {
	var evidence models.OwnerEvidence

	var heartbeat models.WorkerHeartbeat
	err := database.DB.WithContext(ctx).Where("worker_id = ?", attempt.WorkerId).Order("occurred_at DESC").First(&heartbeat).Error
	if err == nil {
		evidence.LastHeartbeatAt = &heartbeat.OccurredAt
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false
	}

	var worker models.Worker
	err = database.DB.WithContext(ctx).Where("worker_id = ?", attempt.WorkerId).Order("started_at DESC").First(&worker).Error
	if err == nil {
		evidence.CurrentInstanceStartedAt = &worker.StartedAt
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false
	}

	return models.AttemptOwnerGone(attempt.ClaimedAt, evidence, time.Now().UTC(), workerHeartbeatFreshnessWindow)
}

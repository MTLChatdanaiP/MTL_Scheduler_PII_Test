package events

import (
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
	"context"
	"time"

	"github.com/oklog/ulid/v2"
)

func MarkAttemptClaimed(ctx context.Context, jobId string, workerId string, attemptNumber int) models.Attempt {
	// RFC-001 §9 Commands: MarkAttemptClaimed
	attempt := models.Attempt{
		AttemptId:     ulid.Make().String(),
		JobId:         jobId,
		WorkerId:      workerId,
		Status:        "Claimed",
		AttemptNumber: attemptNumber,
		ClaimedAt:     time.Now().UTC(),
	}
	database.DB.WithContext(ctx).Create(&attempt)

	// RFC-005 §5: attempt.* used to exist only as entries the timeline synthesized from this table at
	// read time. Now they are real events carrying the attempt id and the worker id.
	LogEventWith(ctx, jobId, "attempt.claimed", "worker", attemptContext(&attempt))
	return attempt
}

func MarkAttemptStarted(ctx context.Context, attempt *models.Attempt) {
	// RFC-001 §9 Commands: MarkAttemptStarted
	attempt.Status = "Started"
	attempt.StartedAt = time.Now().UTC()
	database.DB.WithContext(ctx).Save(attempt)
	LogEventWith(ctx, attempt.JobId, "attempt.started", "worker", attemptContext(attempt))
}

func MarkAttemptSucceeded(ctx context.Context, attempt *models.Attempt) {
	// RFC-001 §9 Commands: MarkAttemptSucceeded
	attempt.Status = "Succeeded"
	attempt.FinishedAt = time.Now().UTC()
	database.DB.WithContext(ctx).Save(attempt)
	LogEventWith(ctx, attempt.JobId, "attempt.succeeded", "worker", attemptContext(attempt))
}

func MarkAttemptAbandoned(ctx context.Context, attempt *models.Attempt) {
	// RFC-001 §9 Commands: MarkAttemptAbandoned. An attempt is abandoned when its owner is gone, so with no better reason given the
	// cause is recorded as WORKER_FAILURE (RFC-001 §14) rather than left empty.
	MarkAttemptAbandonedFor(ctx, attempt, models.FailureWorkerFailure)
}

// MarkAttemptAbandonedFor abandons an attempt whose ownership was lost and records why
// (RFC-001 §6: ABANDONED is for lost ownership; §14: the cause is usually WORKER_FAILURE).
func MarkAttemptAbandonedFor(ctx context.Context, attempt *models.Attempt, category string) {
	attempt.Status = "Abandoned"
	attempt.FinishedAt = time.Now().UTC()
	attempt.FailureCategory = models.NormalizeFailureCategory(category)
	database.DB.WithContext(ctx).Save(attempt)
	LogEventWith(ctx, attempt.JobId, "attempt.abandoned", "worker", attemptContext(attempt))
}

func MarkAttemptFailed(ctx context.Context, attempt *models.Attempt, category string) {
	// RFC-001 §9 Commands: MarkAttemptFailed
	attempt.Status = "Failed"
	attempt.FinishedAt = time.Now().UTC()
	// RFC-001 §14: a failed attempt always carries a real category; empty/unknown becomes UNKNOWN
	attempt.FailureCategory = models.NormalizeFailureCategory(category)
	database.DB.WithContext(ctx).Save(attempt)
	LogEventWith(ctx, attempt.JobId, "attempt.failed", "worker", attemptContext(attempt))
}

func MarkRunQueued(ctx context.Context, task *models.Task) {
	task.Status = "Queued"
	database.DB.WithContext(ctx).Save(task)
	LogEvent(ctx, task.JobId, "task.queued", "scheduler")
}

func MarkRunRunning(ctx context.Context, task *models.Task) {
	// RFC-001 §5 Run State Model: RUNNING
	task.Status = "Running"
	database.DB.WithContext(ctx).Save(task)
	// RFC-000 §5.3: attempt.started-equivalent event
	LogEvent(ctx, task.JobId, "task.started", "worker")
}

func MarkRunCompleted(ctx context.Context, task *models.Task) {
	task.Status = "Completed"
	task.FinishedAt = time.Now().UTC()
	database.DB.WithContext(ctx).Save(task)
	// RFC-000 §5.3: attempt.succeeded-equivalent event
	LogEvent(ctx, task.JobId, "task.completed", "worker")
}

func MarkRunRetryableFailure(ctx context.Context, task *models.Task) {
	//RFC-001 §5: "A failed parent run
	// remains terminal after its retry child is created"
	task.Status = "Failed"
	task.FinishedAt = time.Now().UTC()
	database.DB.WithContext(ctx).Save(task)

	LogEvent(ctx, task.JobId, "task.retry_scheduled", "worker")
}

func MarkRunFailed(ctx context.Context, task *models.Task) {
	task.Status = "Failed"
	task.FinishedAt = time.Now().UTC()
	database.DB.WithContext(ctx).Save(task)
	LogEvent(ctx, task.JobId, "task.failed", "worker")
}

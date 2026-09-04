package events

import (
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
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
	return attempt
}

func MarkAttemptStarted(ctx context.Context, attempt *models.Attempt) {
	// RFC-001 §9 Commands: MarkAttemptStarted
	attempt.Status = "Started"
	attempt.StartedAt = time.Now().UTC()
	database.DB.WithContext(ctx).Save(attempt)
}

func MarkAttemptSucceeded(ctx context.Context, attempt *models.Attempt) {
	// RFC-001 §9 Commands: MarkAttemptSucceeded
	attempt.Status = "Succeeded"
	attempt.FinishedAt = time.Now().UTC()
	database.DB.WithContext(ctx).Save(attempt)
}

func MarkAttemptAbandoned(ctx context.Context, attempt *models.Attempt) {
	// RFC-001 §9 Commands: MarkAttemptAbandoned
	attempt.Status = "Abandoned"
	attempt.FinishedAt = time.Now().UTC()
	database.DB.WithContext(ctx).Save(attempt)
}

func MarkAttemptFailed(ctx context.Context, attempt *models.Attempt, category string) {
	// RFC-001 §9 Commands: MarkAttemptFailed
	attempt.Status = "Failed"
	attempt.FinishedAt = time.Now().UTC()
	attempt.FailureCategory = category
	database.DB.WithContext(ctx).Save(attempt)
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

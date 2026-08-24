package worker

import (
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/events"
	"MTL_Scheduler_PII_Test/internals/models"
	"context"
	"log/slog"
	"time"
)

// RFC-002 §7 Scheduling Flow: "Determine occurrence due -> Create occurrence -> Request Job Run creation -> Associate run_id with occurrence." This project collapses that into a single poll loop over Task rows
const schedulerInterval = 10 * time.Second

// StartScheduler periodically checks for tasks whose RunAt has arrived
// and publishes them to the stream.
// RFC-002 §10 Scheduler Restart / Catch-Up: because due-ness is computed fresh from durable Postgres state on every poll, this loop exhibits CATCH_UP_ALL behavior automatically after a restart — SKIP_MISSED/CATCH_UP_LATEST are not implemented
func StartScheduler(ctx context.Context) {
	for {
		select { // RFC-004 §10 Graceful Shutdown: scheduler stops publishing new due tasks once shutdown is signaled
		case <-ctx.Done():
			return
		default:
		}
		var tasks []models.Task

		result := database.DB.WithContext(ctx).
			// RFC-002 §6 Core Invariant: "the scheduler must not intentionally create multiple logical runs" for one occurrence — the status="Pending" guard prevents re-publishing a task that was already queued on a previous poll
			Where("run_at <= ? AND status = ?", time.Now(), "Pending").
			Find(&tasks)

		if result.Error == nil {
			for _, task := range tasks {
				// RFC-001 §5 Run State Model: QUEUED — separate from RUNNING, which the worker sets independently in ProcessTask
				// RFC-001 §9 Commands: MarkRunQueued
				task.Status = "Queued"
				database.DB.WithContext(ctx).Save(&task)
				// RFC-000 §5.3 Domain Events Are Facts: run.queued-equivalent event, Producer="scheduler" identifies which component emitted it (RFC-005 §5 envelope field)
				events.LogEvent(ctx, task.JobId, "task.queued", "scheduler")

				slog.Info("task queued", "job_id", task.JobId)
				// RFC-003 §6 Delivery Lifecycle: PUBLISHED stage
				PublishToStream(ctx, task.JobId)
			}
		}

		time.Sleep(schedulerInterval)
	}
}

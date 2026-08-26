package worker

import (
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/events"
	"MTL_Scheduler_PII_Test/internals/models"
	"MTL_Scheduler_PII_Test/internals/taskservice"
	"context"
	"fmt"
	"log/slog"
	"time"
)

// RFC-002 §7 Scheduling Flow: "Determine occurrence due -> Create occurrence -> Request Job Run creation -> Associate run_id with occurrence." This project collapses that into a single poll loop over Task rows
const schedulerInterval = 10 * time.Second

// StartScheduler periodically checks for tasks whose RunAt has arrived
// and publishes them to the stream.
// RFC-002 §10 Scheduler Restart / Catch-Up: because due-ness is computed fresh from durable Postgres state on every poll, this loop exhibits CATCH_UP_ALL behavior automatically after a restart — SKIP_MISSED/CATCH_UP_LATEST are not implemented
func StartScheduler(ctx context.Context, scheduler_id string) {
	// RFC-002 §13 Failure Semantics: scheduler now reports its own liveness via
	// StartHeartbeat (registered as worker_id="scheduler"), same mechanism as
	// task workers — a gap in worker_heartbeats for "scheduler" is direct
	// evidence of scheduler downtime, distinguishable from any task-level failure.
	schedulerStruct := CreateWorker(ctx, scheduler_id)
	schedulerInstId := schedulerStruct.InstanceId
	go StartHeartbeat(ctx, scheduler_id, schedulerInstId)

	for {
		select { // RFC-004 §10 Graceful Shutdown: scheduler stops publishing new due tasks once shutdown is signaled
		case <-ctx.Done():
			return
		default:
		}
		var tasks []models.Task

		result := database.DB.WithContext(ctx).
			// RFC-002 §6 Core Invariant: "the scheduler must not intentionally create multiple logical runs" for one occurrence — the status="Pending" guard prevents re-publishing a task that was already queued on a previous poll
			Where("run_at <= ? AND status = ?", time.Now().UTC(), "Pending").
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

		var schedule_defs []models.ScheduleDefinition

		time_table := database.DB.WithContext(ctx).
			Where("next_run_at <= ? AND enabled = ?", time.Now().UTC(), true).
			Find(&schedule_defs)

		if time_table.Error == nil {
			for _, def := range schedule_defs {
				var task models.Task

				expected := def.NextRunAt
				def.NextRunAt = time.Now().UTC().Add(time.Duration(def.IntervalSeconds) * time.Second)

				task.TaskName = def.TaskName
				task.TaskType = def.TaskType
				task.Payload = def.Payload
				task.ExpectedAt = expected

				result := taskservice.CreateTask_Direct(ctx, task)

				fmt.Println("idk log results here IG", result)

				database.DB.WithContext(ctx).Save(&def)
			}
		}

		var overdueSchedules []models.ScheduleDefinition
		cutoff := time.Now().UTC().Add(-missedOccurrenceThreshold)

		overdue_err := database.DB.WithContext(ctx).
			Where("enabled = ?", true).
			Where("next_run_at < ?", cutoff).
			Find(&overdueSchedules).Error

		if overdue_err != nil {
			fmt.Println("No overdued:", overdue_err)
		}

		for _, sched := range overdueSchedules {
			slog.Warn("schedule occurrence missed", "schedule_id", sched.ScheduleId, "next_run_at", sched.NextRunAt)
		}

		time.Sleep(schedulerInterval)
	}
}

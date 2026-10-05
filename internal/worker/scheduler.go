package worker

import (
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/events"
	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/taskservice"
	"context"
	"log/slog"
	"time"
)

// RFC-002 §7 Scheduling Flow: "Determine occurrence due -> Create occurrence -> Request Job Run creation -> Associate run_id with occurrence." This project collapses that into a single poll loop over Task rows
const schedulerInterval = 10 * time.Second

// StartScheduler periodically checks for tasks whose RunAt has arrived
// and publishes them to the stream.
// RFC-002 §10 Scheduler Restart / Catch-Up: because due-ness is computed fresh from durable Postgres state on every poll, this loop exhibits CATCH_UP_ALL behavior automatically after a restart — SKIP_MISSED/CATCH_UP_LATEST are not implemented
func StartScheduler(ctx context.Context, scheduler_id string) {
	schedulerStruct := CreateComponent(ctx, scheduler_id, "Scheduler")
	schedulerInstId := schedulerStruct.InstanceId
	go StartHeartbeat(ctx, scheduler_id, schedulerInstId)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		publishDueTasks(ctx)
		fireRecurringSchedules(ctx)

		time.Sleep(schedulerInterval)
	}
}

// RFC-002 §6 Core Invariant: "the scheduler must not intentionally create
// multiple logical runs" for one occurrence — the status="Pending" guard
// prevents re-publishing a task that was already queued on a previous poll
func publishDueTasks(ctx context.Context) {
	var tasks []models.Task

	result := database.DB.WithContext(ctx).
		Where("run_at <= ? AND status = ?", time.Now().UTC(), "Pending").
		Find(&tasks)

	if result.Error != nil {
		return
	}

	for _, task := range tasks {
		events.MarkRunQueued(ctx, &task)

		slog.Info("task queued", "job_id", task.JobId)
		// RFC-003 §6 Delivery Lifecycle: PUBLISHED stage
		PublishToStream(ctx, task.JobId)
	}
}

func fireRecurringSchedules(ctx context.Context) {
	var schedule_defs []models.ScheduleDefinition

	time_table := database.DB.WithContext(ctx).
		Where("next_run_at <= ? AND enabled = ?", time.Now().UTC(), true).
		Find(&schedule_defs)

	if time_table.Error != nil {
		return
	}

	for _, def := range schedule_defs {
		expected := def.NextRunAt
		nextRunAt := time.Now().UTC().Add(time.Duration(def.IntervalSeconds) * time.Second)

		// RFC-002 §6 Core Invariant:
		// Claiming BEFORE creating the task
		claim := database.DB.WithContext(ctx).
			Model(&models.ScheduleDefinition{}).
			Where("schedule_id = ? AND next_run_at = ?", def.ScheduleId, expected).
			Update("next_run_at", nextRunAt)

		if claim.Error != nil {
			slog.Error("failed to claim recurring schedule occurrence", "schedule_id", def.ScheduleId, "error", claim.Error)
			continue
		}
		if claim.RowsAffected == 0 {
			slog.Info("recurring schedule occurrence already claimed elsewhere, skipping", "schedule_id", def.ScheduleId)
			continue
		}

		var task models.Task
		task.TaskName = def.TaskName
		task.TaskType = def.TaskType
		task.Payload = def.Payload
		task.ExpectedAt = expected
		task.ScheduleId = def.ScheduleId

		result := taskservice.CreateTask_Direct(ctx, task)
		slog.Info("recurring schedule fired", "schedule_id", def.ScheduleId, "job_id", result.JobId)
	}
}

package worker

import (
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/events"
	"MTL_Scheduler_PII_Test/internals/models"
	"MTL_Scheduler_PII_Test/internals/taskservice"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/oklog/ulid/v2"
)

// RFC-002 §7 Scheduling Flow: "Determine occurrence due -> Create occurrence -> Request Job Run creation -> Associate run_id with occurrence." This project collapses that into a single poll loop over Task rows
const schedulerInterval = 10 * time.Second

// StartScheduler periodically checks for tasks whose RunAt has arrived
// and publishes them to the stream.
// RFC-002 §10 Scheduler Restart / Catch-Up: because due-ness is computed fresh from durable Postgres state on every poll, this loop exhibits CATCH_UP_ALL behavior automatically after a restart — SKIP_MISSED/CATCH_UP_LATEST are not implemented
func StartScheduler(ctx context.Context, scheduler_id string) {
	schedulerStruct := CreateWorker(ctx, scheduler_id)
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
		checkOverdueSchedules(ctx)

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

func checkOverdueSchedules(ctx context.Context) {
	var overdueSchedules []models.ScheduleDefinition
	cutoff := time.Now().UTC().Add(-missedOccurrenceThreshold)

	overdue_err := database.DB.WithContext(ctx).
		Where("enabled = ?", true).
		Where("next_run_at < ?", cutoff).
		Find(&overdueSchedules).Error

	if overdue_err != nil {
		fmt.Println("No overdued:", overdue_err)
		return
	}

	for _, sched := range overdueSchedules {
		var existing models.MonitoringAnnotation
		notFoundErr := database.DB.WithContext(ctx).
			Where("subject_id = ? AND type = ? AND resolved_at IS NULL", sched.ScheduleId, "SCHEDULE_MISSED").
			First(&existing).Error

		if notFoundErr != nil {
			evidenceMap := map[string]interface{}{
				"next_run_at": sched.NextRunAt,
			}
			evidenceJSON, _ := json.Marshal(evidenceMap)

			annotation := models.MonitoringAnnotation{
				AnnotationID: ulid.Make().String(),
				Type:         "SCHEDULE_MISSED",
				SubjectType:  "SCHEDULE",
				SubjectID:    sched.ScheduleId,
				DerivedAt:    time.Now().UTC(),
				Evidence:     string(evidenceJSON),
			}
			database.DB.WithContext(ctx).Create(&annotation)
		}

		slog.Warn("schedule occurrence missed", "schedule_id", sched.ScheduleId, "next_run_at", sched.NextRunAt)
	}
}

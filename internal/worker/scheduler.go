package worker

import (
	"MTL_Scheduler_PII_Test/internal/cache"
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
	defer MarkStopped(schedulerInstId) // RFC-010 §9
	markDrainingOnShutdown(ctx, schedulerInstId)
	go StartHeartbeat(ctx, scheduler_id, schedulerInstId)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		publishDueTasks(ctx)
		watchScheduleDefinitions(ctx)
		fireRecurringSchedules(ctx)

		if !sleepCtx(ctx, schedulerInterval) { // stops at once on shutdown instead of finishing the sleep first
			return
		}
	}
}

// RFC-002 §6 Core Invariant: "the scheduler must not intentionally create
// multiple logical runs" for one occurrence — the status="Pending" guard
// prevents re-publishing a task that was already queued on a previous poll
func publishDueTasks(ctx context.Context) {
	// RFC-003 §1: if Redis cannot be reached, touch nothing. Tasks stay Pending, so nothing is
	// lost and no events are produced for work that cannot be delivered. Before this, a task was
	// marked Queued first and the failed publish was swallowed, so a task that came due during a
	// Redis outage was marked Queued, never delivered, and never picked up again.
	if !redisReachable(ctx) {
		return
	}

	var tasks []models.Task

	result := database.DB.WithContext(ctx).
		Where("run_at <= ? AND status = ?", time.Now().UTC(), "Pending").
		Find(&tasks)

	if result.Error != nil {
		return
	}

	queueAndPublish(ctx, tasks)

	// RFC-003 §1: anything that was marked Queued but never confirmed published is published now
	publishStrandedTasks(ctx, findStranded(ctx))
}

// strandedAfter is how long a task may stay Queued without a confirmed publish before it is
// treated as stranded. A var so a test can shorten it.
var strandedAfter = 30 * time.Second

// redisReachable takes a context and returns whether Redis answers a ping, by pinging it. If
// it does not, it records at most one redis.unavailable event per window (the shared limiter),
// so a long outage does not write one event per poll.
func redisReachable(ctx context.Context) bool {
	err := cache.Client.Ping(ctx).Err()
	if err == nil {
		return true
	}

	slog.Warn("redis is unreachable, leaving due tasks Pending until it is back", "error", err)

	if cache.IsUnavailable(err) {
		events.LogEventEvery(ctx, events.RedisDownEventEvery, "system", "redis.unavailable", "scheduler")
	}
	return false
}

// queueAndPublish takes due tasks and, for each, marks it Queued and publishes it.
//
// The order is deliberate. Publishing first would be unsafe: MarkRunQueued does Save() on a task
// read earlier, so running it after a worker had started the task would overwrite Running with
// Queued. If the publish fails the task simply stays Queued with no PublishedAt, which is exactly
// what publishStrandedTasks looks for.
func queueAndPublish(ctx context.Context, tasks []models.Task) {
	for _, task := range tasks {
		events.MarkRunQueued(ctx, &task)

		slog.Info("task queued", "job_id", task.JobId)
		// RFC-003 §6 Delivery Lifecycle: PUBLISHED stage
		if err := publishTask(ctx, task); err != nil {
			slog.Error("publish failed, the task stays Queued and will be republished", "job_id", task.JobId, "error", err)
			events.LogEvent(ctx, task.JobId, "task.publish_failed", "scheduler")
		}
	}
}

// publishTask takes a task and returns an error unless it was published AND recorded as
// published, by sending its envelope and then setting PublishedAt. UpdateColumn is used so the
// task's updated_at is not bumped by the bookkeeping.
func publishTask(ctx context.Context, task models.Task) error {
	env := cache.DeliveryEnvelope{
		JobID:         task.JobId,
		CorrelationID: task.ExecutionChainId,
		TraceID:       task.TraceID,
	}
	streamID, err := PublishEnvelopeID(ctx, env)
	if err != nil {
		return err
	}

	// RFC-005 §16: Redis has CONFIRMED the publish, and this records the stream message id it got, so the attempt that
	// later claims that message can be matched to it
	events.LogEventWith(ctx, task.JobId, "task.published", "scheduler", events.EventContext{StreamPosition: streamID})

	return database.DB.WithContext(ctx).
		Model(&models.Task{}).
		Where("job_id = ?", task.JobId).
		UpdateColumn("published_at", time.Now().UTC()).Error
}

// findStranded takes a context and returns the tasks that are Queued, have no confirmed publish,
// and were last updated more than strandedAfter ago, oldest first, at most 100 at a time.
func findStranded(ctx context.Context) []models.Task {
	var tasks []models.Task

	cutoff := time.Now().UTC().Add(-strandedAfter)

	err := database.DB.WithContext(ctx).
		Where("status = ? AND published_at IS NULL AND updated_at < ?", "Queued", cutoff).
		Order("updated_at ASC").
		Limit(100).
		Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

// publishStrandedTasks takes stranded tasks and publishes each one again. A duplicate delivery
// is safe: delivery is at-least-once and ProcessTask already ignores a delivery for a task that
// is running or finished.
func publishStrandedTasks(ctx context.Context, tasks []models.Task) {
	for _, task := range tasks {
		if err := publishTask(ctx, task); err != nil {
			slog.Error("republishing a stranded task failed, it will be tried again", "job_id", task.JobId, "error", err)
			events.LogEvent(ctx, task.JobId, "task.publish_failed", "scheduler")
			continue
		}

		slog.Warn("republished a task that was Queued but never published", "job_id", task.JobId)
		events.LogEvent(ctx, task.JobId, "task.republished", "scheduler")
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
		fireSchedule(ctx, def)
	}
}

// fireSchedule takes one due schedule definition, claims its occurrence, creates the run, and announces it
// (RFC-002 §11): schedule.occurrence_due, then schedule.occurrence_run_created, and one
// schedule.occurrence_skipped for each occurrence that was passed over.
//
// "Passed over" is real: after downtime this fires ONE occurrence and sets the next run to now + interval, so the
// others are never created. That used to leave no trace at all.
func fireSchedule(ctx context.Context, def models.ScheduleDefinition) {
	expected := def.NextRunAt
	now := time.Now().UTC()
	interval := time.Duration(def.IntervalSeconds) * time.Second
	nextRunAt := now.Add(interval)

	// RFC-002 §6 Core Invariant:
	// Claiming BEFORE creating the task
	claim := database.DB.WithContext(ctx).
		Model(&models.ScheduleDefinition{}).
		Where("schedule_id = ? AND next_run_at = ?", def.ScheduleId, expected).
		Update("next_run_at", nextRunAt)

	if claim.Error != nil {
		slog.Error("failed to claim recurring schedule occurrence", "schedule_id", def.ScheduleId, "error", claim.Error)
		return
	}
	if claim.RowsAffected == 0 {
		slog.Info("recurring schedule occurrence already claimed elsewhere, skipping", "schedule_id", def.ScheduleId)
		return
	}

	// only the instance that won the claim announces it, so each occurrence is announced once
	occurrence := occurrenceID(def.ScheduleId, expected)
	ec := events.EventContext{ScheduleID: def.ScheduleId, ScheduleOccurrenceID: occurrence}
	events.LogEventWith(ctx, def.ScheduleId, "schedule.occurrence_due", "scheduler", ec)

	var task models.Task
	task.TaskName = def.TaskName
	task.TaskType = def.TaskType
	task.Payload = def.Payload
	task.ExpectedAt = expected
	task.ScheduleId = def.ScheduleId
	task.ScheduleOccurrenceId = occurrence

	result := taskservice.CreateTask_Direct(ctx, task)
	slog.Info("recurring schedule fired", "schedule_id", def.ScheduleId, "job_id", result.JobId)

	if result.JobId != "" {
		events.LogEventWith(ctx, result.JobId, "schedule.occurrence_run_created", "scheduler", ec)
	}

	for _, at := range skippedOccurrences(expected, now, interval, maxSkippedEvents) {
		events.LogEventWith(ctx, def.ScheduleId, "schedule.occurrence_skipped", "scheduler", events.EventContext{
			ScheduleID: def.ScheduleId, ScheduleOccurrenceID: occurrenceID(def.ScheduleId, at),
		})
	}
	if total := skippedCount(expected, now, interval); total > 0 {
		slog.Warn("recurring schedule skipped missed occurrences", "schedule_id", def.ScheduleId, "skipped", total, "recorded_as_events", min(total, maxSkippedEvents))
	}
}

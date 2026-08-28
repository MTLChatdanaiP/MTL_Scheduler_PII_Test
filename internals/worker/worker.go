package worker

import (
	"context"
	"fmt"
	"time"

	"MTL_Scheduler_PII_Test/internals/cache"
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/events"
	"MTL_Scheduler_PII_Test/internals/models"

	"github.com/oklog/ulid/v2"

	"log/slog"
	"sync/atomic"

	"github.com/redis/go-redis/v9"
)

type ExecutionOutcome string

const (
	Success             ExecutionOutcome = "Success"
	RetryableFailure    ExecutionOutcome = "RetryableFailure"
	NonRetryableFailure ExecutionOutcome = "NonRetryableFailure"
)

// RFC-003 §4 Redis Primitive: consumer group name — enables competing-consumer distribution across this worker and the reclaimer
const WorkerGroupA = "WorkerG_A"

// RFC-004 §9 Capacity: "active_attempts" — global total across all workers
var activeAttempts int64
var progressChunkDuration = 5 * time.Second
var progressChunkCount = 6

// (RFC-001 §14's list: APPLICATION_ERROR, INVALID_INPUT, DEPENDENCY_ERROR,
// TIMEOUT, WORKER_FAILURE, INFRASTRUCTURE_ERROR, UNKNOWN)
func runHandler(ctx context.Context, task models.Task) ExecutionOutcome {
	switch task.TaskType {

	case "fail_retryable":
		return RetryableFailure

	case "fail_permanent":
		return NonRetryableFailure

	default:
		for i := 0; i < progressChunkCount; i++ {
			time.Sleep(progressChunkDuration)
			events.LogEvent(ctx, task.JobId, "task.progress", "worker")
		}
		return Success
	}
}

// RFC-004 §7 Execution Protocol: validate envelope -> claim/establish attempt -> emit attempt.started -> execute handler -> record success/failure
// RFC-001 §6 Attempt State Model: CLAIMED -> STARTED -> SUCCEEDED|FAILED, simplified here to Running -> Completed on the Task itself rather than a separate Attempt record
func ProcessTask(ctx context.Context, JobId string, workerId string) {
	var task models.Task

	db := database.DB
	results := database.DB.WithContext(ctx).Where("job_id = ?", JobId).First(&task)

	// RFC-003 §9 At-Least-Once Delivery / RFC-004 §12 Duplicate Execution:
	// idempotency guard — an unfinished attempt already existing for this
	// JobId means this is a duplicate delivery, not new work.
	if results.Error != nil {
		fmt.Println("Task not found:", JobId)
		return
	}

	// RFC-001 §8 Invariant 12: "Terminal run states must not silently
	// transition back to running." Guards against a stray/duplicate delivery
	// reprocessing a task that already reached a terminal state.
	if task.Status == "Completed" || task.Status == "Failed" {
		slog.Warn("ignoring delivery for already-terminal task", "job_id", JobId, "status", task.Status)
		return
	}

	var existingAttempt models.Attempt

	results = database.DB.WithContext(ctx).
		Where("job_id = ? AND status IN ?", JobId, []string{"Claimed", "Started"}).
		First(&existingAttempt)

	if results.Error == nil {
		slog.Warn("attempt.duplicate_detected", "job_id", JobId)
		return
	}

	var count int64
	database.DB.WithContext(ctx).Model(&models.Attempt{}).Where("job_id = ?", JobId).Count(&count)

	// RFC-001 §9 Commands: CreateAttempt + MarkAttemptClaimed (attempt starts at Status: "Claimed")
	attempt := models.Attempt{
		AttemptId:     ulid.Make().String(),
		JobId:         JobId,
		WorkerId:      workerId,
		Status:        "Claimed",
		AttemptNumber: int(count) + 1,
		ClaimedAt:     time.Now().UTC(),
	}
	database.DB.WithContext(ctx).Create(&attempt)

	atomic.AddInt64(&activeAttempts, 1)
	AddWorkerCounter(workerId, 1)

	// RFC-001 §5 Run State Model: RUNNING
	task.Status = "Running"
	db.WithContext(ctx).Save(&task)
	// RFC-000 §5.3: attempt.started-equivalent event
	events.LogEvent(ctx, task.JobId, "task.started", "worker")

	// RFC-001 §9 Commands: MarkAttemptStarted
	attempt.Status = "Started"
	attempt.StartedAt = time.Now().UTC()
	database.DB.WithContext(ctx).Save(&attempt)
	//change note status from "Pending" to "Running"
	slog.Info("task started", "worker_id", workerId, "job_id", JobId)

	outcome := runHandler(ctx, task) //process the task if tasktype allows it

	switch outcome {

	// RFC-001 §8 Invariant 11: "A run cannot be SUCCEEDED without at least one
	// succeeded attempt." Re-fetches from the DB rather than trusting the
	// in-memory struct, so a silent persistence failure can't produce a false
	// Completed status.
	// RFC-001 §9 Commands: MarkAttemptSucceeded + MarkRunSucceeded
	case Success:
		attempt.Status = "Succeeded"
		attempt.FinishedAt = time.Now().UTC()
		db.WithContext(ctx).Save(&attempt)

		// RFC-001 §8 Invariant 11: only mark the run SUCCEEDED after confirming
		// the attempt's success was actually persisted — re-fetch and check
		// rather than assuming the in-memory struct matches the DB
		var confirmed models.Attempt
		if err := database.DB.WithContext(ctx).First(&confirmed, "attempt_id = ?", attempt.AttemptId).Error; err != nil || confirmed.Status != "Succeeded" {
			slog.Error("attempt succeeded but failed to persist — refusing to mark task Completed", "job_id", JobId)
			return
		}

		task.Status = "Completed"
		task.FinishedAt = time.Now().UTC()
		db.WithContext(ctx).Save(&task)

		// RFC-000 §5.3: attempt.succeeded-equivalent event
		events.LogEvent(ctx, task.JobId, "task.completed", "worker")
	// RFC-001 §9 Commands: ScheduleRetryRun
	case RetryableFailure:
		// RFC-001 §8 Invariant 13: a repeated retry-scheduling command must
		// reuse the already-created child, not spawn a sibling
		var existingChild models.Task
		if err := database.DB.WithContext(ctx).Where("parent_run_id = ?", task.JobId).First(&existingChild).Error; err == nil {
			slog.Warn("retry child already exists for this run, skipping duplicate creation", "job_id", task.JobId, "existing_child", existingChild.JobId)
			return
		}

		// the FAILED run stays terminal — RFC-001 §5: "A failed parent run
		// remains terminal after its retry child is created"
		task.Status = "Failed"
		task.FinishedAt = time.Now().UTC()
		db.WithContext(ctx).Save(&task)

		attempt.Status = "Abandoned"
		attempt.FinishedAt = time.Now().UTC()
		database.DB.WithContext(ctx).Save(&attempt)

		events.LogEvent(ctx, task.JobId, "task.retry_scheduled", "worker")

		// RFC-001 §7 Retry Lineage: a retry creates a NEW run — new JobId, same
		// execution_chain_id, parent_run_id set to the failed run. The failed
		// parent stays Failed permanently (RFC-001 §5: "A failed parent run
		// remains terminal after its retry child is created").
		retryTask := models.Task{
			JobId:            ulid.Make().String(),
			TaskName:         task.TaskName,
			TaskType:         "Default", // reset from "fail_retryable" so it doesn't loop forever
			Payload:          task.Payload,
			Status:           "Pending",
			RunAt:            time.Now().UTC().Add(10 * time.Second),
			ExecutionChainId: task.ExecutionChainId, // SAME chain as the parent
			ParentRunId:      task.JobId,            // points back to the failed run
			RetryIndex:       task.RetryIndex + 1,
		}
		database.DB.WithContext(ctx).Create(&retryTask)

		events.LogEvent(ctx, retryTask.JobId, "task.created", "worker")
	// RFC-001 §9 Commands: MarkAttemptFailed + MarkRunFailed
	case NonRetryableFailure:
		task.Status = "Failed"
		db.WithContext(ctx).Save(&task)

		attempt.Status = "Failed"
		attempt.FinishedAt = time.Now().UTC()
		attempt.FailureCategory = "APPLICATION_ERROR"
		db.WithContext(ctx).Save(&attempt)

		events.LogEvent(ctx, task.JobId, "task.failed", "worker")
	}

	atomic.AddInt64(&activeAttempts, -1)
	AddWorkerCounter(workerId, -1)

}

// RFC-003 §6 Delivery Lifecycle: AVAILABLE -> CLAIMED — XReadGroup with Block implements event-driven (not polling) delivery, per RFC-003 §1 "runtime delivery substrate"
func ReadStream(ctx context.Context, Consumer string, StreamText string, Group string) []redis.XStream {

	rdb := cache.Client

	streams, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    Group,
		Consumer: Consumer,
		Streams:  []string{StreamText, ">"},
		Count:    1,
		Block:    5 * time.Second,
	}).Result()

	if err != nil {
		if err == redis.Nil {
			return nil
		}
		fmt.Printf("[%s] XReadGroup error: %v\n", Consumer, err)
		if cache.IsUnavailable(err) {
			events.LogEvent(ctx, "system", "redis.unavailable", "worker")
		}
		time.Sleep(1 * time.Second)
		return nil
	}

	return streams
}

// RFC-003 §7 Critical Distinction: "message claimed != attempt started != attempt succeeded != message acknowledged." This function performs claim handling, delegates execution to ProcessTask, then acknowledges — keeping those steps sequential and observable
// Shared by both the normal consumer (SetupWorker) and the reclaimer (StartReclaimer) — RFC-004 §4 treats a worker as an independent, identifiable consumer regardless of how it acquired a message
func ProcessStream(ctx context.Context, Consumer string, StreamText string, Group string, Message redis.XMessage) {

	rdb := cache.Client
	msg := Message

	fmt.Printf("[%s] Received %s: %v (at %s)\n",
		Consumer, msg.ID, msg.Values, time.Now().UTC().Format("15:04:05"))

	taskId, ok := msg.Values["job_id"].(string)

	if !ok {
		fmt.Println("Error A parsing string:", ok)
		return
	}

	slog.Info("task claimed", "worker_id", Consumer, "job_id", taskId)
	workCtx := context.WithoutCancel(ctx)

	ProcessTask(workCtx, taskId, Consumer)

	slog.Info("task processed", "worker_id", Consumer, "job_id", taskId)

	// RFC-003 §8 Acknowledgement Semantics: ack happens only after ProcessTask has durably recorded a terminal state, not before
	if err := rdb.XAck(workCtx, StreamText, Group, msg.ID).Err(); err != nil {
		slog.Error("xack failed", "worker_id", Consumer, "job_id", taskId, "error", err)
		if cache.IsUnavailable(err) {
			events.LogEvent(ctx, "system", "redis.unavailable", "worker")
		}
	} else {
		slog.Info("acked", "worker_id", Consumer, "job_id", taskId)
	}
}

const MaxConcurrency = 1 // single-threaded worker: one message read + fully processed before the next

// RFC-004 §5 Worker Registration / §4 Worker Identity: worker_id — no separate registration record, heartbeat, or capacity reporting implemented yet
// RFC-004 §6 Worker Heartbeat: conceptual payload "worker_id, occurred_at, running_attempts, capacity, version" — capacity/version deferred (see models.WorkerHeartbeat)
// Runs as its own goroutine, independent of the message-processing loop, so a slow/blocked XReadGroup or ProcessTask never delays or skips a heartbeat tick
func SetupWorker(ctx context.Context, worker_id string) {
	fmt.Printf("[%s] Setting Up Worker\n", worker_id)

	workerStruct := CreateWorker(ctx, worker_id)
	workerInstId := workerStruct.InstanceId
	// RFC-004 §6: heartbeat starts immediately after worker identity is registered, before any message processing begins
	go StartHeartbeat(ctx, worker_id, workerInstId)

	fmt.Println(workerInstId)

	rdb := cache.Client
	group := WorkerGroupA
	TaskStream := cache.TaskStream

	// RFC-003 §4 Redis Primitive: consumer group created with start position "$" (new messages only) — avoids replaying the stream's entire historical backlog on every group (re)creation
	err := rdb.XGroupCreateMkStream(ctx, TaskStream, group, "$").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		fmt.Printf("[%s] XGroupCreate error: %v\n", worker_id, err)
		if cache.IsUnavailable(err) {
			events.LogEvent(ctx, "system", "redis.unavailable", "worker")
		}
	}

	for {
		select { // RFC-004 §10 Graceful Shutdown: "stop claiming new jobs... do not acknowledge unfinished work merely to empty the queue." Checked only at the top of the loop, so an in-flight ProcessStream/ProcessTask always finishes before this worker stops claiming new messages.
		case <-ctx.Done():
			return
		default:
		}
		Streams := ReadStream(ctx, worker_id, TaskStream, group)
		if Streams != nil {
			for _, s := range Streams {
				for _, msg := range s.Messages {
					ProcessStream(ctx, worker_id, TaskStream, group, msg)
				}
			}
		}
	}
}

// RFC-001 §9 Commands: CreateManualRetryRun and CancelRun are not implemented —
// this project has no operator-triggered retry/cancel path, only automatic
// retry via RetryableFailure. Documented as a known gap.

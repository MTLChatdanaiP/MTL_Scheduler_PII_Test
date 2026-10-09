package worker

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"MTL_Scheduler_PII_Test/internal/cache"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/events"
	"MTL_Scheduler_PII_Test/internal/models"

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

// RFC-001 §2 Goals / §11 Retry Semantics: retries must be bounded.
const maxRetries = models.MaxRetries

// RFC-004 §8 Execution Heartbeat. A var, not a const, so a test can shorten it --
var ExecutionHeartbeatInterval = 10 * time.Second

// RFC-004 §9 Capacity: "active_attempts" — global total across all workers
var activeAttempts int64
var progressChunkDuration = 5 * time.Second
var progressChunkCount = 6

// (RFC-001 §14's list: APPLICATION_ERROR, INVALID_INPUT, DEPENDENCY_ERROR,
// TIMEOUT, WORKER_FAILURE, INFRASTRUCTURE_ERROR, UNKNOWN)
// HandlerResult is everything a handler produces: how it ended, why, what it
// returned, and the lines it logged while running.
type HandlerResult struct {
	Outcome  ExecutionOutcome
	Category string
	Output   string
	// Logs are scanned as RFC-006 §8's STRUCTURED_LOG source.
	Logs []string
}

// runHandlerFull takes the task and returns a HandlerResult, by running the
// handler for its task type and collecting what it logs. It is the real
// implementation; runHandler below wraps it.
func runHandlerFull(ctx context.Context, task models.Task) HandlerResult {
	RegisterBuiltinHandlers()

	run := &HandlerRun{}
	run.Log("starting " + task.TaskType + " for " + task.TaskName)

	handler, ok := lookupHandler(task.TaskType)
	if !ok {
		handler = defaultHandler // a task type nobody registered runs the default job, exactly as before
	}
	return invokeHandler(ctx, task, run, handler)
}

// runHandler keeps its original signature, returning the first three fields of
// runHandlerFull, so every existing caller and test is untouched.
func runHandler(ctx context.Context, task models.Task) (ExecutionOutcome, string, string) {
	result := runHandlerFull(ctx, task)
	return result.Outcome, result.Category, result.Output
}

// RFC-004 §7 Execution Protocol: validate envelope -> claim/establish attempt -> emit attempt.started -> execute handler -> record success/failure
// RFC-001 §6 Attempt State Model: CLAIMED -> STARTED -> SUCCEEDED|FAILED, simplified here to Running -> Completed on the Task itself rather than a separate Attempt record
func ProcessTask(ctx context.Context, JobId string, workerId string) {
	var task models.Task

	results := database.DB.WithContext(ctx).Where("job_id = ?", JobId).First(&task)

	// RFC-003 §9 At-Least-Once Delivery / RFC-004 §12 Duplicate Execution:
	// idempotency guard — an unfinished attempt already existing for this
	// JobId means this is a duplicate delivery, not new work.
	if results.Error != nil {
		fmt.Println("Task not found:", JobId)
		return
	}

	// RFC-001 §8 Invariant 12: "Terminal run states must not silently transition back to running."
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
		events.LogEventWith(ctx, JobId, "attempt.duplicate_detected", "worker", events.EventContext{WorkerID: workerId})
		return
	}

	var count int64
	database.DB.WithContext(ctx).Model(&models.Attempt{}).Where("job_id = ?", JobId).Count(&count)

	// RFC-001 §9 Commands: CreateAttempt + MarkAttemptClaimed (attempt starts at Status: "Claimed")
	attempt := events.MarkAttemptClaimed(ctx, JobId, workerId, int(count)+1)

	atomic.AddInt64(&activeAttempts, 1)
	AddWorkerCounter(workerId, 1)

	// RFC-004 §9: released on EVERY way out of this function. It used to be released only at the very bottom, and three
	// early returns (an unpersisted success, a retry child that already exists, retries exhausted) skipped it, so each
	// leaked one slot for good. With MAX_CONCURRENCY=1 a single leak left the worker believing it was full forever.
	defer func() {
		atomic.AddInt64(&activeAttempts, -1)
		AddWorkerCounter(workerId, -1)
	}()

	events.MarkRunRunning(ctx, &task)

	// RFC-001 §9 Commands: MarkAttemptStarted
	events.MarkAttemptStarted(ctx, &attempt)
	slog.Info("task started", "worker_id", workerId, "job_id", JobId)

	// RFC-004 §8: a goroutine ticks out attempt.heartbeat events for as long as
	// runHandler is still running, and stops the instant it returns, success or
	// failure
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(ExecutionHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				events.LogExecutionHeartbeat(ctx, JobId, attempt.AttemptId, workerId)
			}
		}
	}()

	handlerResult := runHandlerFull(ctx, task) //process the task if tasktype allows it
	stopHeartbeat()
	outcome, category, handlerOutput := handlerResult.Outcome, handlerResult.Category, handlerResult.Output

	// RFC-006 §8 STRUCTURED_LOG: what the handler logged is scanned, sanitized and
	// stored like its result, whatever the outcome.
	if logText := joinHandlerLogs(handlerResult.Logs); logText != "" {
		ScanAndPersistArtifact(ctx, JobId, attempt.AttemptId, "STRUCTURED_LOG", logText, task.TaskType, task.Queue)
	}

	switch outcome {

	// RFC-001 §8 Invariant 11: "A run cannot be SUCCEEDED without at least one succeeded attempt."
	// RFC-001 §9 Commands: MarkAttemptSucceeded + MarkRunSucceeded
	case Success:
		// RFC-006 §24
		ScanAndPersistArtifact(ctx, JobId, attempt.AttemptId, "JOB_RESULT", handlerOutput, task.TaskType, task.Queue)

		events.MarkAttemptSucceeded(ctx, &attempt)

		// RFC-001 §8 Invariant 11: only mark the run SUCCEEDED after confirming the attempt's success was actually persisted
		var confirmed models.Attempt
		if err := database.DB.WithContext(ctx).First(&confirmed, "attempt_id = ?", attempt.AttemptId).Error; err != nil || confirmed.Status != "Succeeded" {
			slog.Error("attempt succeeded but failed to persist — refusing to mark task Completed", "job_id", JobId)
			return
		}

		events.MarkRunCompleted(ctx, &task)

	// RFC-001 §9 Commands: ScheduleRetryRun
	case RetryableFailure:
		// RFC-001 §8 Invariant 13: a repeated retry-scheduling command must
		// reuse the already-created child, not spawn a sibling
		var existingChild models.Task
		if err := database.DB.WithContext(ctx).Where("parent_run_id = ?", task.JobId).First(&existingChild).Error; err == nil {
			slog.Warn("retry child already exists for this run, skipping duplicate creation", "job_id", task.JobId, "existing_child", existingChild.JobId)
			return
		}

		//RFC-001 §5: "A failed parent run remains terminal after its retry child is created"
		// RFC-006 §24: the worker's own error output is a candidate artifact and
		// may contain PII the original payload never had. Scanned and sanitized
		// BEFORE anything is persisted; the raw text never leaves this frame.
		ScanAndPersistArtifact(ctx, JobId, attempt.AttemptId, "ERROR_MESSAGE", handlerOutput, task.TaskType, task.Queue)

		events.MarkRunRetryableFailure(ctx, &task)
		// RFC-001 §6: ABANDONED is for lost ownership (transport recovery, see recovery.go);
		// a handler that returned an error is a FAILED attempt, with its category.
		events.MarkAttemptFailed(ctx, &attempt, category)

		if task.RetryIndex >= maxRetries {
			slog.Warn("retry limit reached, not creating another retry child", "job_id", task.JobId, "retry_index", task.RetryIndex, "max_retries", maxRetries)
			events.LogEvent(ctx, task.JobId, "task.retries_exhausted", "worker")
			return
		}

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
			TraceID:          task.TraceID, // the whole chain shares one trace
		}
		database.DB.WithContext(ctx).Create(&retryTask)

		events.LogEvent(ctx, retryTask.JobId, "task.created", "worker")

	// RFC-001 §9 Commands: MarkAttemptFailed + MarkRunFailed
	case NonRetryableFailure:
		// RFC-006 §24: a permanent failure's error output is scanned too.
		//
		// This call used to sit at the end of the RetryableFailure case above,
		// guarded by `if outcome == NonRetryableFailure`. Inside that case the
		// outcome can never be NonRetryableFailure, so it was dead code and the
		// error output of every permanent failure (invalid input, dependency,
		// timeout, permanent, infrastructure) was never scanned or stored.
		ScanAndPersistArtifact(ctx, JobId, attempt.AttemptId, "ERROR_MESSAGE", handlerOutput, task.TaskType, task.Queue)

		events.MarkAttemptFailed(ctx, &attempt, category)
		events.MarkRunFailed(ctx, &task)

		// RFC-007 §4 RUN_TIMEOUT: a handler that reports a timeout is the one timeout this system can see
		// (it does not enforce a deadline itself), and the alert needs an event to read.
		if models.NormalizeFailureCategory(category) == models.FailureTimeout {
			events.LogEvent(ctx, task.JobId, "task.timed_out", "worker")
		}
	}

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
			events.LogEventEvery(ctx, events.RedisDownEventEvery, "system", "redis.unavailable", "worker")
		}
		// the group vanished (Redis restarted without persistence, or the stream was deleted): create it again, so the NEXT read
		// works. Without this every read fails with NOGROUP until the worker is restarted.
		if cache.IsNoGroup(err) {
			if healErr := ensureGroup(ctx, rdb, StreamText, Group); healErr == nil {
				fmt.Printf("[%s] consumer group %s was missing and has been created again\n", Consumer, Group)
			}
		}
		sleepCtx(ctx, 1*time.Second)
		return nil
	}

	return streams
}

// RFC-003 §7 Critical Distinction: "message claimed != attempt started != attempt succeeded != message acknowledged." This function performs claim handling, delegates execution to ProcessTask, then acknowledges — keeping those steps sequential and observable
// Shared by both the normal consumer (SetupWorker) and the reclaimer (StartReclaimer) — RFC-004 §4 treats a worker as an independent, identifiable consumer regardless of how it acquired a message
func ProcessStream(ctx context.Context, Consumer string, StreamText string, Group string, Message redis.XMessage) {

	msg := Message

	// RFC-003 §14: log the message id, job id and trace id, never the whole message
	fmt.Printf("[%s] Received %s (at %s)\n", Consumer, describeMessage(msg), time.Now().UTC().Format("15:04:05"))

	// RFC-003 §5: the message is read through the envelope parser, not by hand
	env, err := cache.ParseEnvelope(msg.Values)
	if err != nil {
		dropMalformed(ctx, Consumer, StreamText, Group, msg, err)
		return
	}
	taskId := env.JobID

	slog.Info("task claimed", "worker_id", Consumer, "job_id", taskId, "trace_id", env.TraceID)
	// RFC-005 §16: every event written while this delivery is handled records which stream message it was
	workCtx := events.WithStreamPosition(context.WithoutCancel(ctx), msg.ID)

	ProcessTask(workCtx, taskId, Consumer)

	slog.Info("task processed", "worker_id", Consumer, "job_id", taskId, "trace_id", env.TraceID)

	// RFC-003 §8 Acknowledgement Semantics: ack happens only after ProcessTask has durably recorded a terminal state, not before.
	// RFC-003 §14: an acknowledged message is then deleted, so Redis holds only work in flight.
	acked, err := ackAndDelete(workCtx, StreamText, Group, msg.ID)
	if err != nil {
		slog.Error("xack failed", "worker_id", Consumer, "job_id", taskId, "error", err)
		if cache.IsUnavailable(err) {
			events.LogEventEvery(ctx, events.RedisDownEventEvery, "system", "redis.unavailable", "worker")
		}
	} else if acked {
		slog.Info("acked", "worker_id", Consumer, "job_id", taskId)
	}
}

// MaxConcurrency keeps its startup value so code that reads it still compiles. Nothing inside the
// worker uses it any more: it is evaluated before .env is loaded, so use maxConcurrency() instead.
var MaxConcurrency = getEnvIntOrDefault("MAX_CONCURRENCY", 1)

func getEnvIntOrDefault(EnvValue string, defaultInt int) int {
	envint := os.Getenv(EnvValue)
	if envint == "" {
		return defaultInt
	}
	value, err := strconv.Atoi(envint)
	if err != nil {
		return defaultInt
	}
	return value
}

// RFC-004 §5 Worker Registration / §4 Worker Identity: worker_id — no separate registration record, heartbeat, or capacity reporting implemented yet
// RFC-004 §6 Worker Heartbeat: conceptual payload "worker_id, occurred_at, running_attempts, capacity, version" — capacity/version deferred (see models.WorkerHeartbeat)
// Runs as its own goroutine, independent of the message-processing loop, so a slow/blocked XReadGroup or ProcessTask never delays or skips a heartbeat tick
func SetupWorker(ctx context.Context, worker_id string) {
	fmt.Printf("[%s] Setting Up Worker\n", worker_id)

	workerStruct := CreateWorker(ctx, worker_id)
	workerInstId := workerStruct.InstanceId
	defer MarkStopped(workerInstId) // RFC-010 §9: returning from here means shutdown was signalled, not that the process crashed
	markDrainingOnShutdown(ctx, workerInstId) // RFC-010 §9: from the signal until the stop is recorded, it is draining, not missing
	// RFC-004 §6: heartbeat starts immediately after worker identity is registered, before any message processing begins
	go StartHeartbeat(ctx, worker_id, workerInstId)

	fmt.Println(workerInstId)

	rdb := cache.Client
	group := WorkerGroupA
	TaskStream := cache.TaskStream

	// RFC-003 §4 Redis Primitive: consumer group created with start position "$" (new messages only) — avoids replaying the stream's entire historical backlog on every group (re)creation
	// RFC-004 §10: a worker started while Redis is down must not run without a consumer group for ever (the group used to be
	// created ONCE, so every later read failed with NOGROUP until a restart). Retry with backoff until it exists or we are stopped.
	if err := retryWithBackoff(ctx, func() error {
		err := ensureGroup(ctx, rdb, TaskStream, group)
		if err != nil {
			fmt.Printf("[%s] XGroupCreate error: %v\n", worker_id, err)
			if cache.IsUnavailable(err) {
				events.LogEventEvery(ctx, events.RedisDownEventEvery, "system", "redis.unavailable", "worker")
			}
		}
		return err
	}, defaultBackoff()); err != nil {
		return // cancelled while waiting for Redis
	}

	for {
		select { // RFC-004 §10 Graceful Shutdown: "stop claiming new jobs... do not acknowledge unfinished work merely to empty the queue." Checked only at the top of the loop, so an in-flight ProcessStream/ProcessTask always finishes before this worker stops claiming new messages.
		case <-ctx.Done():
			return
		default:
		}

		current, ok := GetWorkerCounter(worker_id)
		if !ok {
			slog.Error("no counter registered for worker, refusing to claim work until this is resolved", "worker_id", worker_id)
			if !sleepCtx(ctx, 999*time.Second) {
				return
			}
			continue
		}
		if current >= int64(maxConcurrency()) {
			if !sleepCtx(ctx, 5*time.Second) {
				return
			}
			continue
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

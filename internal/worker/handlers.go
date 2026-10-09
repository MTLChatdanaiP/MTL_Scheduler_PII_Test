package worker

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"MTL_Scheduler_PII_Test/internal/events"
	"MTL_Scheduler_PII_Test/internal/models"
)

// RFC-004 §11: what runs a task is registered by task type, not hard-wired into one switch.

// HandlerRun collects what a handler logs and builds its result. ProcessTask scans the logs as the STRUCTURED_LOG artifact.
type HandlerRun struct {
	logs []string
}

// Log records one line.
func (r *HandlerRun) Log(line string) { r.logs = append(r.logs, line) }

// Finish ends the run: it adds the closing log line and returns the result.
func (r *HandlerRun) Finish(outcome ExecutionOutcome, category string, output string) HandlerResult {
	r.logs = append(r.logs, "handler finished: "+output)
	return HandlerResult{Outcome: outcome, Category: category, Output: output, Logs: r.logs}
}

// Handler runs one task. ctx is the worker's, task is the stored run, run collects the logs and builds the result.
type Handler func(ctx context.Context, task models.Task, run *HandlerRun) HandlerResult

var (
	handlersMu  sync.RWMutex
	handlers    = map[string]Handler{}
	builtinOnce sync.Once
)

// RegisterHandler makes h the handler for taskType. Registering the same type twice, an empty type or a nil handler PANICS: it is
// a programming mistake that must show up at start-up, not as a task running the wrong code later. Safe for concurrent use.
func RegisterHandler(taskType string, h Handler) {
	if taskType == "" || h == nil {
		panic("worker: RegisterHandler needs a task type and a handler")
	}
	handlersMu.Lock()
	defer handlersMu.Unlock()
	if _, exists := handlers[taskType]; exists {
		panic(fmt.Sprintf("worker: a handler for task type %q is already registered", taskType))
	}
	handlers[taskType] = h
}

func lookupHandler(taskType string) (Handler, bool) {
	handlersMu.RLock()
	defer handlersMu.RUnlock()
	h, ok := handlers[taskType]
	return h, ok
}

// RegisterBuiltinHandlers registers the handlers that ship with the worker. It runs once, however often it is called, and is
// called explicitly by runHandlerFull, so nothing depends on init() ordering.
func RegisterBuiltinHandlers() {
	builtinOnce.Do(func() {
		fail := func(outcome ExecutionOutcome, category, output string) Handler {
			return func(ctx context.Context, task models.Task, run *HandlerRun) HandlerResult {
				return run.Finish(outcome, category, output)
			}
		}
		RegisterHandler("fail_retryable", fail(RetryableFailure, "", "handler reported a transient failure and asked to be retried"))
		RegisterHandler("fail_invalid_input", func(ctx context.Context, task models.Task, run *HandlerRun) HandlerResult {
			return run.Finish(NonRetryableFailure, models.FailureInvalidInput, "payload rejected by handler validation: "+task.Payload)
		})
		RegisterHandler("fail_dependency", fail(NonRetryableFailure, models.FailureDependencyError, "a dependency the handler needed was unavailable"))
		RegisterHandler("fail_timeout", fail(NonRetryableFailure, models.FailureTimeout, "handler exceeded its time budget"))
		RegisterHandler("fail_permanent", fail(NonRetryableFailure, models.FailureApplicationError, "handler failed permanently and will not be retried"))
		RegisterHandler("fail_infrastructure", fail(NonRetryableFailure, models.FailureInfrastructureError, "handler could not reach required infrastructure"))
	})
}

// defaultHandler is what runs for every task type nobody registered: it works in chunks, reports progress for each, and succeeds.
func defaultHandler(ctx context.Context, task models.Task, run *HandlerRun) HandlerResult {
	for i := 0; i < progressChunkCount; i++ {
		time.Sleep(progressChunkDuration)
		events.LogEvent(ctx, task.JobId, "task.progress", "worker")
		run.Log("processed chunk " + strconv.Itoa(i+1) + " of " + strconv.Itoa(progressChunkCount))
	}
	return run.Finish(Success, "", "completed "+task.TaskName+" ("+task.TaskType+")")
}

// invokeHandler runs h and turns a PANIC into a failed run. A handler bug must fail ITS run, not kill the worker (and with it every
// other run on that worker). The panic value is not put in the result or the log: it can contain whatever the handler was holding.
func invokeHandler(ctx context.Context, task models.Task, run *HandlerRun, h Handler) (result HandlerResult) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("a task handler panicked; the run is failed and the worker continues", "task_type", task.TaskType, "job_id", task.JobId, "panic_type", fmt.Sprintf("%T", r))
			result = run.Finish(NonRetryableFailure, models.FailureUnknown, "handler panicked")
		}
	}()
	return h(ctx, task, run)
}

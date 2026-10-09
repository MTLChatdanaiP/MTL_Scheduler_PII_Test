package worker

// Batch 5 (5C): the handler registry. Helper names are prefixed b5.

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internal/models"
)

// b5Register registers a handler under a type nobody else uses and removes it afterwards.
func b5Register(t *testing.T, h Handler) string {
	t.Helper()
	taskType := "b5_type_" + ulid.Make().String()
	RegisterHandler(taskType, h)
	t.Cleanup(func() {
		handlersMu.Lock()
		delete(handlers, taskType)
		handlersMu.Unlock()
	})
	return taskType
}

func b5Panics(f func()) (panicked bool, message string) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			message, _ = r.(string)
		}
	}()
	f()
	return
}

func TestRegisterHandler_ADuplicateRegistrationPanicsAtRegistrationTime(t *testing.T) {
	taskType := b5Register(t, func(ctx context.Context, task models.Task, run *HandlerRun) HandlerResult {
		return run.Finish(Success, "", "first")
	})

	panicked, msg := b5Panics(func() {
		RegisterHandler(taskType, func(ctx context.Context, task models.Task, run *HandlerRun) HandlerResult {
			return run.Finish(Success, "", "second")
		})
	})
	if !panicked || !strings.Contains(msg, taskType) {
		t.Fatalf("a second registration must panic and name the type, got panicked=%v %q", panicked, msg)
	}

	// and the first handler is still the one in place
	got := runHandlerFull(context.Background(), models.Task{TaskType: taskType})
	if got.Output != "first" {
		t.Fatalf("the original registration must survive a rejected duplicate, got %q", got.Output)
	}
}

func TestRegisterHandler_NeedsATypeAndAHandler(t *testing.T) {
	if p, _ := b5Panics(func() {
		RegisterHandler("", func(context.Context, models.Task, *HandlerRun) HandlerResult { return HandlerResult{} })
	}); !p {
		t.Error("an empty task type must panic")
	}
	if p, _ := b5Panics(func() { RegisterHandler("b5_nil_handler", nil) }); !p {
		t.Error("a nil handler must panic")
	}
}

func TestRunHandler_ACustomRegisteredHandlerIsUsed(t *testing.T) {
	taskType := b5Register(t, func(ctx context.Context, task models.Task, run *HandlerRun) HandlerResult {
		run.Log("custom work on " + task.TaskName)
		return run.Finish(Success, "", "custom result")
	})

	got := runHandlerFull(context.Background(), models.Task{TaskType: taskType, TaskName: "n"})
	if got.Outcome != Success || got.Output != "custom result" {
		t.Fatalf("got %+v", got)
	}
	want := []string{"starting " + taskType + " for n", "custom work on n", "handler finished: custom result"}
	if len(got.Logs) != 3 || got.Logs[0] != want[0] || got.Logs[1] != want[1] || got.Logs[2] != want[2] {
		t.Fatalf("logs = %q", got.Logs)
	}
}

// a handler bug must fail ITS run, not kill the worker, and its panic text must not leak into the result
func TestRunHandler_APanickingHandlerFailsTheRunAndNotTheWorker(t *testing.T) {
	taskType := b5Register(t, func(ctx context.Context, task models.Task, run *HandlerRun) HandlerResult {
		panic("could not process jane.doe@example.com")
	})

	var got HandlerResult
	if p, _ := b5Panics(func() { got = runHandlerFull(context.Background(), models.Task{JobId: "b5-panic", TaskType: taskType}) }); p {
		t.Fatal("a panic in a handler escaped and would have killed the worker")
	}
	if got.Outcome != NonRetryableFailure || got.Category != models.FailureUnknown || got.Output != "handler panicked" {
		t.Fatalf("a panicking handler is a failed run with category UNKNOWN: %+v", got)
	}
	for _, line := range append(got.Logs, got.Output) {
		if strings.Contains(line, "jane.doe") {
			t.Fatalf("the panic text leaked into the result: %q", line)
		}
	}
}

func TestRegisterBuiltinHandlers_RunsOnceHoweverOftenItIsCalled(t *testing.T) {
	for i := 0; i < 5; i++ {
		if p, msg := b5Panics(RegisterBuiltinHandlers); p {
			t.Fatalf("call %d panicked: %s", i+1, msg)
		}
	}
	for _, taskType := range []string{"fail_retryable", "fail_invalid_input", "fail_dependency", "fail_timeout", "fail_permanent", "fail_infrastructure"} {
		if _, ok := lookupHandler(taskType); !ok {
			t.Errorf("%s is not registered", taskType)
		}
	}
	for _, taskType := range []string{"Dummy", "Default", "anything_else"} {
		if _, ok := lookupHandler(taskType); ok {
			t.Errorf("%s must NOT be registered: it belongs to the default handler", taskType)
		}
	}
}

func TestRegistry_ConcurrentRegistrationAndLookupAreSafe(t *testing.T) {
	var wg sync.WaitGroup
	types := make([]string, 40)
	for i := range types {
		types[i] = "b5_conc_" + ulid.Make().String()
	}
	t.Cleanup(func() {
		handlersMu.Lock()
		for _, ty := range types {
			delete(handlers, ty)
		}
		handlersMu.Unlock()
	})

	for _, ty := range types {
		wg.Add(2)
		go func(ty string) {
			defer wg.Done()
			RegisterHandler(ty, func(ctx context.Context, task models.Task, run *HandlerRun) HandlerResult {
				return run.Finish(Success, "", ty)
			})
		}(ty)
		go func(ty string) {
			defer wg.Done()
			lookupHandler(ty)
			RegisterBuiltinHandlers()
		}(ty)
	}
	wg.Wait()

	for _, ty := range types {
		if _, ok := lookupHandler(ty); !ok {
			t.Errorf("%s was lost", ty)
		}
	}
}

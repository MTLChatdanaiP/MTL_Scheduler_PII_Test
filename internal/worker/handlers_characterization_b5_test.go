package worker

// Batch 5 (5C): what every EXISTING task type does, pinned down BEFORE the handler switch became a registry and kept as a guard
// afterwards. The exact outcome, failure category, output text and log lines are asserted.

import (
	"context"
	"strconv"
	"testing"
	"time"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func b5FastProgress(t *testing.T) {
	t.Helper()
	count, duration := progressChunkCount, progressChunkDuration
	progressChunkCount, progressChunkDuration = 3, time.Millisecond
	t.Cleanup(func() { progressChunkCount, progressChunkDuration = count, duration })
}

func TestHandlers_EveryFailureFixtureKeepsItsExactBehaviour(t *testing.T) {
	cases := []struct {
		taskType string
		outcome  ExecutionOutcome
		category string
		output   string
	}{
		{"fail_retryable", RetryableFailure, "", "handler reported a transient failure and asked to be retried"},
		{"fail_invalid_input", NonRetryableFailure, "INVALID_INPUT", `payload rejected by handler validation: {"x":1}`},
		{"fail_dependency", NonRetryableFailure, "DEPENDENCY_ERROR", "a dependency the handler needed was unavailable"},
		{"fail_timeout", NonRetryableFailure, "TIMEOUT", "handler exceeded its time budget"},
		{"fail_permanent", NonRetryableFailure, "APPLICATION_ERROR", "handler failed permanently and will not be retried"},
		{"fail_infrastructure", NonRetryableFailure, "INFRASTRUCTURE_ERROR", "handler could not reach required infrastructure"},
	}
	for _, c := range cases {
		got := runHandlerFull(context.Background(), models.Task{JobId: "b5-char-" + c.taskType, TaskName: "my task", TaskType: c.taskType, Payload: `{"x":1}`})

		if got.Outcome != c.outcome || got.Category != c.category || got.Output != c.output {
			t.Errorf("%s: got outcome=%v category=%q output=%q\n want outcome=%v category=%q output=%q", c.taskType, got.Outcome, got.Category, got.Output, c.outcome, c.category, c.output)
		}
		wantLogs := []string{"starting " + c.taskType + " for my task", "handler finished: " + c.output}
		if len(got.Logs) != 2 || got.Logs[0] != wantLogs[0] || got.Logs[1] != wantLogs[1] {
			t.Errorf("%s: logs = %q, want %q", c.taskType, got.Logs, wantLogs)
		}
	}
}

func TestHandlers_AnyOtherTypeRunsTheDefaultJobAndSucceeds(t *testing.T) {
	b5FastProgress(t)
	for _, taskType := range []string{"Dummy", "Default", "some_future_type", ""} {
		jobID := "b5-char-default-" + strconv.Itoa(len(taskType)) + taskType
		t.Cleanup(func() { database.DB.Unscoped().Where("job_id = ?", jobID).Delete(&models.EventEnvelope{}) })

		got := runHandlerFull(context.Background(), models.Task{JobId: jobID, TaskName: "work", TaskType: taskType})

		wantOutput := "completed work (" + taskType + ")"
		if got.Outcome != Success || got.Category != "" || got.Output != wantOutput {
			t.Errorf("type %q: outcome=%v category=%q output=%q", taskType, got.Outcome, got.Category, got.Output)
		}
		// starting + 3 chunks + finished
		if len(got.Logs) != 5 || got.Logs[1] != "processed chunk 1 of 3" || got.Logs[3] != "processed chunk 3 of 3" || got.Logs[4] != "handler finished: "+wantOutput {
			t.Errorf("type %q: logs = %q", taskType, got.Logs)
		}
		var progress int64
		database.DB.Model(&models.EventEnvelope{}).Where("job_id = ? AND event_type = ?", jobID, "task.progress").Count(&progress)
		if progress != 3 {
			t.Errorf("type %q: one task.progress event per chunk, got %d", taskType, progress)
		}
	}
}

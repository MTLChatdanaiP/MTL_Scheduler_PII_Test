package worker

// RFC-004 §9 Capacity. ProcessTask increments the attempt counters after claiming an attempt, and used to decrement
// them only at the very bottom. Three early returns skip the bottom, so each leaked one slot permanently. With
// MAX_CONCURRENCY=1 a single leak means the worker believes it is full and never claims another task.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

// counters returns the worker's own counter and the process-wide one.
func counters(t *testing.T, workerID string) (int64, int64) {
	t.Helper()
	w, ok := GetWorkerCounter(workerID)
	if !ok {
		t.Fatalf("worker counter for %s is not registered", workerID)
	}
	return w, atomic.LoadInt64(&activeAttempts)
}

func runAndMeasure(t *testing.T, taskType string, prepare func(models.Task)) (leakedWorker int64, leakedGlobal int64) {
	t.Helper()
	useTestPolicy(t, artifactPolicy())
	fastProgress(t)

	workerID := "leak-worker-" + ulid.Make().String()
	RegisterWorkerCounter(workerID)
	t.Cleanup(func() { DeleteWorkerCounter(workerID) })

	task := newRunnableTask(t, taskType, "n", `{}`)
	if prepare != nil {
		prepare(task)
	}

	w0, g0 := counters(t, workerID)
	ProcessTask(context.Background(), task.JobId, workerID)
	w1, g1 := counters(t, workerID)
	return w1 - w0, g1 - g0
}

func TestCapacity_EveryNormalOutcomeReleasesItsSlot(t *testing.T) {
	// the regression guard: these always worked
	for _, taskType := range []string{"Dummy", "fail_permanent", "fail_retryable"} {
		w, g := runAndMeasure(t, taskType, nil)
		if w != 0 || g != 0 {
			t.Errorf("%s left the counters at worker %+d / global %+d, want 0", taskType, w, g)
		}
	}
}

func TestCapacity_RetriesExhaustedReleasesItsSlot(t *testing.T) {
	w, g := runAndMeasure(t, "fail_retryable", func(task models.Task) {
		database.DB.Model(&models.Task{}).Where("job_id = ?", task.JobId).UpdateColumn("retry_index", maxRetries)
	})
	if w != 0 || g != 0 {
		t.Fatalf("a run that exhausted its retries leaked a slot: worker %+d, global %+d. With MAX_CONCURRENCY=1 the worker would never claim again", w, g)
	}
}

func TestCapacity_ARetryChildThatAlreadyExistsReleasesItsSlot(t *testing.T) {
	w, g := runAndMeasure(t, "fail_retryable", func(task models.Task) {
		child := models.Task{JobId: ulid.Make().String(), TaskName: "n", TaskType: "fail_retryable", Status: "Pending", ParentRunId: task.JobId, ExecutionChainId: task.ExecutionChainId}
		database.DB.Create(&child)
		t.Cleanup(func() { cleanupJobRows(child.JobId) })
	})
	if w != 0 || g != 0 {
		t.Fatalf("a duplicate retry-child path leaked a slot: worker %+d, global %+d", w, g)
	}
}

func TestCapacity_ASuccessThatCouldNotBePersistedReleasesItsSlot(t *testing.T) {
	// make every attempts UPDATE fail, so the attempt never reaches "Succeeded" and ProcessTask refuses to complete
	database.DB.Callback().Update().Before("gorm:update").Register("test:block_attempt_update", func(db *gorm.DB) {
		if db.Statement.Table == "attempts" {
			db.AddError(errors.New("blocked by the test"))
		}
	})
	t.Cleanup(func() { database.DB.Callback().Update().Remove("test:block_attempt_update") })

	w, g := runAndMeasure(t, "Dummy", nil)
	if w != 0 || g != 0 {
		t.Fatalf("the unpersisted-success path leaked a slot: worker %+d, global %+d", w, g)
	}
}

func TestCapacity_AWorkerWithOneSlotCanStillClaimAfterAnExhaustedRun(t *testing.T) {
	t.Setenv("MAX_CONCURRENCY", "1")
	useTestPolicy(t, artifactPolicy())

	workerID := "leak-worker-" + ulid.Make().String()
	RegisterWorkerCounter(workerID)
	t.Cleanup(func() { DeleteWorkerCounter(workerID) })

	task := newRunnableTask(t, "fail_retryable", "n", `{}`)
	database.DB.Model(&models.Task{}).Where("job_id = ?", task.JobId).UpdateColumn("retry_index", maxRetries)
	ProcessTask(context.Background(), task.JobId, workerID)

	current, _ := GetWorkerCounter(workerID)
	if current >= int64(maxConcurrency()) {
		t.Fatalf("after one exhausted run the worker's counter is %d with a limit of %d: it would refuse every future task", current, maxConcurrency())
	}
}

package taskservice

// RFC-003 §5: one trace id follows a job from creation through Redis to the worker's logs.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"MTL_Scheduler_PII_Test/internal/cache"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func TestCreateTask_GeneratesATraceIDWhenNoneIsGiven(t *testing.T) {
	useTestPolicy(t, testPolicy())

	got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{}`})

	if !cache.ValidTraceID(got.TraceID) {
		t.Fatalf("expected a generated, valid trace id, got %q", got.TraceID)
	}
	var stored models.Task
	database.DB.Where("job_id = ?", got.JobId).First(&stored)
	if stored.TraceID != got.TraceID {
		t.Fatalf("the stored trace id %q differs from the returned one %q", stored.TraceID, got.TraceID)
	}
}

func TestCreateTask_KeepsAValidTraceIDFromTheCaller(t *testing.T) {
	useTestPolicy(t, testPolicy())

	got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{}`, TraceID: "caller-trace.42"})

	if got.TraceID != "caller-trace.42" {
		t.Fatalf("a valid caller trace id should be kept, got %q", got.TraceID)
	}
}

// The trace id travels into Redis and into every log line. If any text were accepted it would be
// a way to put free text, and therefore PII, in both: the same reason TaskName is scanned.
func TestCreateTask_ReplacesAnUnsafeTraceID(t *testing.T) {
	useTestPolicy(t, testPolicy())

	unsafe := []string{"jane.doe@example.com", "has space", "line\nbreak", strings.Repeat("a", 65), "<script>", "555-123-4567 call me"}
	for _, bad := range unsafe {
		got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{}`, TraceID: bad})

		if got.TraceID == bad {
			t.Errorf("the unsafe trace id %q was accepted", bad)
		}
		if !cache.ValidTraceID(got.TraceID) {
			t.Errorf("the replacement for %q is not itself valid: %q", bad, got.TraceID)
		}
	}
}

func TestCreateTask_TwoTasksGetDifferentTraceIDs(t *testing.T) {
	useTestPolicy(t, testPolicy())

	a := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{}`})
	b := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{}`})

	if a.TraceID == b.TraceID {
		t.Fatalf("two jobs shared a trace id: %q", a.TraceID)
	}
}

// PublishedAt is the scheduler's record that Redis confirmed a publish. Task binds from the request
// body, so a caller could send one; the recovery sweep would then believe an unpublished task was
// already published and it would never run.
func TestCreateTask_PublishedAtCannotBeSetByTheCaller(t *testing.T) {
	useTestPolicy(t, testPolicy())

	forged := time.Now().UTC()
	got := create(t, models.Task{TaskName: "n", TaskType: "Dummy", Payload: `{}`, PublishedAt: &forged})

	if got.PublishedAt != nil {
		t.Fatal("the returned task claims it was already published")
	}
	var stored models.Task
	database.DB.Where("job_id = ?", got.JobId).First(&stored)
	if stored.PublishedAt != nil {
		t.Fatal("the stored task claims it was already published, so the recovery sweep would skip it and it would be lost")
	}
}

func jsonOf(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

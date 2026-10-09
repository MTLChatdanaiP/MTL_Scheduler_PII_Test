package events

// RFC-005 §5 Monitoring Event Envelope: correlation fields "where applicable".

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func makeTask(t *testing.T, mod func(*models.Task)) models.Task {
	t.Helper()
	task := models.Task{JobId: ulid.Make().String(), TaskName: "n", TaskType: "Dummy", Status: "Pending"}
	task.ExecutionChainId = "chain-" + task.JobId
	if mod != nil {
		mod(&task)
	}
	if err := database.DB.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		database.DB.Unscoped().Where("job_id = ?", task.JobId).Delete(&models.Task{})
		database.DB.Unscoped().Where("job_id = ?", task.JobId).Delete(&models.EventEnvelope{})
		database.DB.Unscoped().Where("job_id = ?", task.JobId).Delete(&models.RunProjection{})
		database.DB.Unscoped().Where("job_id = ?", task.JobId).Delete(&models.Attempt{})
	})
	return task
}

func lastEvent(t *testing.T, subject, eventType string) models.EventEnvelope {
	t.Helper()
	var e models.EventEnvelope
	if err := database.DB.Where("job_id = ? AND event_type = ?", subject, eventType).Order("id DESC").First(&e).Error; err != nil {
		t.Fatalf("no %s event for %s: %v", eventType, subject, err)
	}
	return e
}

func forgetSubject(t *testing.T, subject string) {
	t.Cleanup(func() {
		database.DB.Unscoped().Where("job_id = ?", subject).Delete(&models.EventEnvelope{})
		database.DB.Unscoped().Where("job_id = ?", subject).Delete(&models.RunProjection{})
	})
}

func TestLogEvent_ARunEventCarriesTheRunsCorrelationFields(t *testing.T) {
	task := makeTask(t, func(k *models.Task) {
		k.ParentRunId = "parent-1"
		k.RetryIndex = 2
		k.TraceID = "trace-A"
		k.ScheduleId = "sched-1"
		k.ScheduleOccurrenceId = "sched-1:1760000000"
		k.Queue = "tasks:stream"
	})

	LogEvent(context.Background(), task.JobId, "task.created", "test")

	e := lastEvent(t, task.JobId, "task.created")
	if e.ExecutionChainID != task.ExecutionChainId || e.ParentRunID != "parent-1" || e.RetryIndex != 2 {
		t.Fatalf("lineage fields wrong: %+v", e)
	}
	if e.TraceID != "trace-A" {
		t.Fatalf("trace_id = %q, want trace-A", e.TraceID)
	}
	if e.CorrelationID != task.ExecutionChainId {
		t.Fatalf("correlation_id = %q, want the execution chain id %q (the same value Redis carries)", e.CorrelationID, task.ExecutionChainId)
	}
	if e.ScheduleID != "sched-1" || e.ScheduleOccurrenceID != "sched-1:1760000000" || e.QueueName != "tasks:stream" {
		t.Fatalf("schedule/queue fields wrong: %+v", e)
	}
}

func TestLogEventWith_AnExplicitContextBeatsTheTask(t *testing.T) {
	task := makeTask(t, func(k *models.Task) { k.ScheduleId = "sched-from-task"; k.Queue = "queue-from-task" })

	LogEventWith(context.Background(), task.JobId, "task.created", "test", EventContext{
		ScheduleID: "sched-explicit", QueueName: "queue-explicit", AttemptID: "att-1", WorkerID: "w-1",
	})

	e := lastEvent(t, task.JobId, "task.created")
	if e.ScheduleID != "sched-explicit" || e.QueueName != "queue-explicit" {
		t.Fatalf("what the caller says must win over what the task says: %+v", e)
	}
	if e.AttemptID != "att-1" || e.WorkerID != "w-1" {
		t.Fatalf("attempt and worker ids were not carried: %+v", e)
	}
}

func TestLogEvent_ANonRunEventSetsItsOwnFieldFromTheSubject(t *testing.T) {
	tests := []struct {
		subject, eventType string
		field              func(models.EventEnvelope) string
		name               string
	}{
		{"worker-" + ulid.Make().String(), "worker.offline", func(e models.EventEnvelope) string { return e.WorkerID }, "worker_id"},
		{"queue-" + ulid.Make().String(), "queue.degraded", func(e models.EventEnvelope) string { return e.QueueName }, "queue_name"},
		{"sched-" + ulid.Make().String(), "schedule.missed", func(e models.EventEnvelope) string { return e.ScheduleID }, "schedule_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forgetSubject(t, tt.subject)
			LogEvent(context.Background(), tt.subject, tt.eventType, "test")

			e := lastEvent(t, tt.subject, tt.eventType)
			if got := tt.field(e); got != tt.subject {
				t.Fatalf("%s = %q, want %q", tt.name, got, tt.subject)
			}
			if e.ExecutionChainID != "" || e.TraceID != "" || e.CorrelationID != "" {
				t.Fatalf("a non-run event must not claim a run's lineage: %+v", e)
			}
		})
	}
}

func TestLogEvent_SystemEventsCarryNoCorrelation(t *testing.T) {
	for _, et := range []string{"pii.policy_activated", "redis.unavailable", "delivery.malformed"} {
		LogEvent(context.Background(), "system", et, "test-system-"+et)
		var e models.EventEnvelope
		database.DB.Where("producer = ?", "test-system-"+et).Order("id DESC").First(&e)
		t.Cleanup(func() {
			database.DB.Unscoped().Where("producer = ?", "test-system-"+et).Delete(&models.EventEnvelope{})
		})
		if e.EventType != et || e.ExecutionChainID != "" || e.WorkerID != "" || e.QueueName != "" || e.ScheduleID != "" {
			t.Fatalf("%s should carry no correlation, got %+v", et, e)
		}
	}
}

func TestLogEvent_ScheduleOccurrenceRunCreatedBelongsToARun(t *testing.T) {
	task := makeTask(t, func(k *models.Task) { k.ScheduleId = "sched-9"; k.ScheduleOccurrenceId = "sched-9:100" })

	LogEvent(context.Background(), task.JobId, "schedule.occurrence_run_created", "test")

	e := lastEvent(t, task.JobId, "schedule.occurrence_run_created")
	if e.ScheduleID != "sched-9" || e.ScheduleOccurrenceID != "sched-9:100" || e.ExecutionChainID != task.ExecutionChainId {
		t.Fatalf("this event's subject IS a run, so it must carry the run's fields: %+v", e)
	}
}

func TestLogEvent_AnEventForAnUnknownRunStillWrites(t *testing.T) {
	subject := "ghost-" + ulid.Make().String()
	forgetSubject(t, subject)

	LogEvent(context.Background(), subject, "task.created", "test")

	e := lastEvent(t, subject, "task.created")
	if e.ExecutionChainID != "" || e.TraceID != "" {
		t.Fatalf("there is no task, so there is nothing to copy: %+v", e)
	}
}

// RFC-005 §5 cost: LogEvent used to read the task for EVERY event, including worker, queue and "system"
// events whose subject can never be a task. The count is measured with a database callback, not assumed.
func TestLogEvent_OnlyRunScopedEventsLookUpTheTask(t *testing.T) {
	var taskQueries int64
	database.DB.Callback().Query().Before("gorm:query").Register("test:count_task_queries", func(db *gorm.DB) {
		if db.Statement.Table == "tasks" {
			atomic.AddInt64(&taskQueries, 1)
		}
	})
	t.Cleanup(func() { database.DB.Callback().Query().Remove("test:count_task_queries") })

	run := makeTask(t, nil)
	nonRun := "w-" + ulid.Make().String()
	forgetSubject(t, nonRun)

	count := func(subject, eventType string) int64 {
		atomic.StoreInt64(&taskQueries, 0)
		LogEvent(context.Background(), subject, eventType, "test-lookup")
		return atomic.LoadInt64(&taskQueries)
	}

	for _, et := range []string{"worker.offline", "queue.degraded", "schedule.created", "component.offline", "monitoring.degraded", "redis.unavailable", "alert.opened", "retention.pruned"} {
		if n := count(nonRun, et); n != 0 {
			t.Errorf("%s made %d task lookups, want 0: its subject cannot be a run", et, n)
		}
	}
	if n := count("system", "pii.policy_activated"); n != 0 {
		t.Errorf(`a "system" event made %d task lookups, want 0`, n)
	}
	if n := count(run.JobId, "task.created"); n != 1 {
		t.Errorf("a run event should read the task exactly once, made %d lookups", n)
	}
	if n := count(run.JobId, "something.unrecognised"); n != 1 {
		t.Errorf("an unrecognised family is looked up (the safe default), made %d lookups", n)
	}
	database.DB.Unscoped().Where("producer = ?", "test-lookup").Delete(&models.EventEnvelope{})
}

func TestEventEnvelope_OldEventsStillSerialiseWithoutTheNewFields(t *testing.T) {
	b, _ := json.Marshal(models.EventEnvelope{JobId: "j", EventType: "task.created"})
	for _, key := range []string{"schedule_id", "schedule_occurrence_id", "queue_name", "trace_id", "correlation_id"} {
		if strings.Contains(string(b), key) {
			t.Errorf("an event that has no %s must not emit an empty key (older API consumers): %s", key, b)
		}
	}
}

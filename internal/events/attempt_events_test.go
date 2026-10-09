package events

// RFC-005 §5, RFC-010 §7: attempt.claimed/started/finished used to exist only as entries the timeline
// synthesized from the attempts table at read time. They are real events now, carrying the attempt id
// and the worker id (RFC-005 §5's "where applicable").

import (
	"context"
	"testing"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func countEvents(t *testing.T, subject, eventType string) int64 {
	t.Helper()
	var n int64
	database.DB.Model(&models.EventEnvelope{}).Where("job_id = ? AND event_type = ?", subject, eventType).Count(&n)
	return n
}

func TestAttemptLifecycle_EveryTransitionIsARealEventWithTheAttemptAndWorkerIds(t *testing.T) {
	ctx := context.Background()
	task := makeTask(t, func(k *models.Task) { k.TraceID = "trace-attempt" })

	attempt := MarkAttemptClaimed(ctx, task.JobId, "worker-77", 1)
	MarkAttemptStarted(ctx, &attempt)
	MarkAttemptSucceeded(ctx, &attempt)

	for _, et := range []string{"attempt.claimed", "attempt.started", "attempt.succeeded"} {
		if n := countEvents(t, task.JobId, et); n != 1 {
			t.Fatalf("%s: expected exactly one event, found %d", et, n)
		}
		e := lastEvent(t, task.JobId, et)
		if e.AttemptID != attempt.AttemptId || e.WorkerID != "worker-77" {
			t.Fatalf("%s must carry the attempt id and worker id, got attempt=%q worker=%q", et, e.AttemptID, e.WorkerID)
		}
		if e.ExecutionChainID != task.ExecutionChainId || e.TraceID != "trace-attempt" {
			t.Fatalf("%s must also carry the run's lineage and trace: %+v", et, e)
		}
	}

	claimed, started, succeeded := lastEvent(t, task.JobId, "attempt.claimed"), lastEvent(t, task.JobId, "attempt.started"), lastEvent(t, task.JobId, "attempt.succeeded")
	if !(claimed.ID < started.ID && started.ID < succeeded.ID) {
		t.Fatal("the events must be recorded in lifecycle order")
	}
}

func TestAttemptFailed_IsAnEvent(t *testing.T) {
	ctx := context.Background()
	task := makeTask(t, nil)
	attempt := MarkAttemptClaimed(ctx, task.JobId, "worker-1", 1)

	MarkAttemptFailed(ctx, &attempt, "APPLICATION_ERROR")

	e := lastEvent(t, task.JobId, "attempt.failed")
	if e.AttemptID != attempt.AttemptId || e.WorkerID != "worker-1" {
		t.Fatalf("attempt.failed must identify the attempt: %+v", e)
	}
}

func TestAttemptAbandoned_IsAnEventOnBothPaths(t *testing.T) {
	ctx := context.Background()
	task := makeTask(t, nil)

	a1 := MarkAttemptClaimed(ctx, task.JobId, "worker-1", 1)
	MarkAttemptAbandoned(ctx, &a1)
	a2 := MarkAttemptClaimed(ctx, task.JobId, "worker-2", 2)
	MarkAttemptAbandonedFor(ctx, &a2, "WORKER_FAILURE")

	if n := countEvents(t, task.JobId, "attempt.abandoned"); n != 2 {
		t.Fatalf("both abandon paths must record attempt.abandoned, found %d", n)
	}
}

func TestAttemptEvents_AreRealEventsNotJustTimelineEntries(t *testing.T) {
	// the point of the change: they are in event_envelopes, where monitoring and the live feed read
	ctx := context.Background()
	task := makeTask(t, nil)
	attempt := MarkAttemptClaimed(ctx, task.JobId, "worker-1", 1)
	MarkAttemptStarted(ctx, &attempt)

	var types []string
	database.DB.Model(&models.EventEnvelope{}).Where("job_id = ? AND event_type LIKE ?", task.JobId, "attempt.%").Order("id").Pluck("event_type", &types)

	if len(types) != 2 || types[0] != "attempt.claimed" || types[1] != "attempt.started" {
		t.Fatalf("expected claimed then started in the event log, got %v", types)
	}
}

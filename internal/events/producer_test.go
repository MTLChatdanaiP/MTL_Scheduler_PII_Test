package events

// RFC-005 §16 Ordering fields.

import (
	"context"
	"strings"
	"testing"
	"time"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func TestProducerSequence_StrictlyIncreasesAndNamesTheProcess(t *testing.T) {
	var last int64
	for i := 0; i < 100; i++ {
		id, seq := nextProducerStamp("worker")
		if seq <= last {
			t.Fatalf("sequence went from %d to %d", last, seq)
		}
		last = seq
		if !strings.HasPrefix(id, "worker:") || !strings.HasSuffix(id, producerInstance) {
			t.Fatalf("producer id %q should be '<producer>:<this process>'", id)
		}
	}
}

func TestEventLess_IsATotalOrderWhoseTiesFollowTheProducerSequence(t *testing.T) {
	now := time.Now().UTC()
	// equal times; A and B from one producer with the sequence AGAINST the database id; C from another producer
	a := models.EventEnvelope{ProducerID: "p:1", ProducerSequence: 1, OccurredAt: now}
	a.ID = 3
	b := models.EventEnvelope{ProducerID: "p:1", ProducerSequence: 2, OccurredAt: now}
	b.ID = 1
	c := models.EventEnvelope{ProducerID: "q:1", ProducerSequence: 1, OccurredAt: now}
	c.ID = 2

	want := []uint{3, 1, 2} // p:1 seq1, p:1 seq2, then q:1
	for _, order := range [][]models.EventEnvelope{{a, b, c}, {c, b, a}, {b, a, c}, {b, c, a}, {a, c, b}, {c, a, b}} {
		evs := append([]models.EventEnvelope{}, order...)
		SortEvents(evs)
		got := []uint{evs[0].ID, evs[1].ID, evs[2].ID}
		if got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
			t.Fatalf("every starting order must sort identically (a cycle would not), got ids %v want %v", got, want)
		}
	}
}

func TestEventLess_TimeLeadsAcrossProducers(t *testing.T) {
	early := models.EventEnvelope{ProducerID: "z:9", ProducerSequence: 99, OccurredAt: time.Now().Add(-time.Hour)}
	late := models.EventEnvelope{ProducerID: "a:1", ProducerSequence: 1, OccurredAt: time.Now()}
	if !EventLess(early, late) || EventLess(late, early) {
		t.Fatal("events from different processes can only be compared by time")
	}
}

func TestEveryEventRecordsItsProducerAndSequence(t *testing.T) {
	task := makeTask(t, nil)
	LogEvent(context.Background(), task.JobId, "task.created", "api")
	LogEvent(context.Background(), task.JobId, "task.queued", "scheduler")
	LogExecutionHeartbeat(context.Background(), task.JobId, "att-1", "w-1")

	var rows []models.EventEnvelope
	database.DB.Where("job_id = ?", task.JobId).Order("id").Find(&rows)
	if len(rows) != 3 {
		t.Fatalf("expected 3 events, got %d", len(rows))
	}
	if !strings.HasPrefix(rows[0].ProducerID, "api:") || !strings.HasPrefix(rows[1].ProducerID, "scheduler:") || !strings.HasPrefix(rows[2].ProducerID, "worker:") {
		t.Fatalf("each event names its producer: %q %q %q", rows[0].ProducerID, rows[1].ProducerID, rows[2].ProducerID)
	}
	if !(rows[0].ProducerSequence < rows[1].ProducerSequence && rows[1].ProducerSequence < rows[2].ProducerSequence) || rows[0].ProducerSequence == 0 {
		t.Fatalf("sequences must be set and increasing: %d %d %d", rows[0].ProducerSequence, rows[1].ProducerSequence, rows[2].ProducerSequence)
	}
}

func TestStreamPosition_ReachesEveryEventWrittenWhileHandlingADelivery(t *testing.T) {
	task := makeTask(t, nil)
	ctx := WithStreamPosition(context.Background(), "1700000000000-7")

	LogEvent(ctx, task.JobId, "task.started", "worker")
	LogEvent(ctx, task.JobId, "task.completed", "worker")
	LogEvent(context.Background(), task.JobId, "task.created", "api") // not part of that delivery

	pos := func(eventType string) string { return lastEvent(t, task.JobId, eventType).StreamPosition }
	if pos("task.started") != "1700000000000-7" || pos("task.completed") != "1700000000000-7" {
		t.Fatal("events written while handling a delivery must carry its stream position")
	}
	if pos("task.created") != "" {
		t.Fatal("an event outside any delivery must not claim one")
	}
}

func TestStreamPosition_AnExplicitValueBeatsTheContext(t *testing.T) {
	task := makeTask(t, nil)
	ctx := WithStreamPosition(context.Background(), "1-1")

	LogEventWith(ctx, task.JobId, "task.published", "scheduler", EventContext{StreamPosition: "2-2"})

	if got := lastEvent(t, task.JobId, "task.published").StreamPosition; got != "2-2" {
		t.Fatalf("explicit stream position = %q, want 2-2", got)
	}
}

func TestSortEvents_UsesTheDatabaseIDOnlyAsTheLastTieBreak(t *testing.T) {
	t0 := time.Now().UTC()
	x := models.EventEnvelope{ProducerID: "p:1", ProducerSequence: 5, OccurredAt: t0.Add(time.Second)}
	y := models.EventEnvelope{ProducerID: "p:1", ProducerSequence: 6, OccurredAt: t0}
	evs := []models.EventEnvelope{x, y}
	SortEvents(evs)
	if evs[0].ProducerSequence != 6 {
		t.Fatal("a clock that stepped backwards still orders by time for display; the sequence is retained for investigation, and projections do not depend on this order")
	}
}

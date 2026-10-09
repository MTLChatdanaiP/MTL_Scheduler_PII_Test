package handlers

// RFC-005 §5: real attempt events must not make the timeline show each attempt twice.

import (
	"testing"
	"time"

	"MTL_Scheduler_PII_Test/internal/models"
)

func attemptAt(id string, claimed, started, finished bool, status string) models.Attempt {
	base := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	a := models.Attempt{AttemptId: id, JobId: "run-1", WorkerId: "w-1", AttemptNumber: 1, Status: status}
	if claimed {
		a.ClaimedAt = base
	}
	if started {
		a.StartedAt = base.Add(time.Second)
	}
	if finished {
		a.FinishedAt = base.Add(2 * time.Second)
	}
	return a
}

func ev(eventType, attemptID string) models.EventEnvelope {
	return models.EventEnvelope{JobId: "run-1", EventType: eventType, AttemptID: attemptID, WorkerID: "w-1", OccurredAt: time.Now()}
}

func countType(entries []TimelineEntry, eventType string) int {
	n := 0
	for _, e := range entries {
		if e.EventType == eventType {
			n++
		}
	}
	return n
}

func TestTimeline_ANewRunShowsEachAttemptStepOnce(t *testing.T) {
	attempts := []models.Attempt{attemptAt("A1", true, true, true, "Succeeded")}
	events := []models.EventEnvelope{ev("attempt.claimed", "A1"), ev("attempt.started", "A1"), ev("attempt.succeeded", "A1")}

	eventEntries := timelineFromEvents(events)
	merged := append(append([]TimelineEntry{}, eventEntries...), dropCoveredAttemptEntries(eventEntries, timelineFromAttempts(attempts))...)

	// 3 real events, and NO synthesized duplicates of them
	if len(merged) != 3 {
		t.Fatalf("expected 3 entries (the real events only), got %d: %+v", len(merged), merged)
	}
	for _, kind := range []string{"attempt.claimed", "attempt.started"} {
		if n := countType(merged, kind); n != 1 {
			t.Errorf("%s appears %d times, want 1", kind, n)
		}
	}
}

func TestTimeline_ARunFromBeforeTheRealEventsKeepsItsSynthesizedEntries(t *testing.T) {
	attempts := []models.Attempt{attemptAt("OLD", true, true, true, "Failed")}

	kept := dropCoveredAttemptEntries(nil, timelineFromAttempts(attempts))

	if len(kept) != 3 {
		t.Fatalf("an old run has no real events, so all 3 synthesized entries must stay, got %d", len(kept))
	}
}

func TestTimeline_OnlyTheCoveredStepsAreDropped(t *testing.T) {
	attempts := []models.Attempt{attemptAt("A1", true, true, true, "Succeeded")}
	eventEntries := timelineFromEvents([]models.EventEnvelope{ev("attempt.claimed", "A1")})

	kept := dropCoveredAttemptEntries(eventEntries, timelineFromAttempts(attempts))

	if countType(kept, "attempt.claimed") != 0 {
		t.Error("claimed is covered by a real event and must be dropped")
	}
	if countType(kept, "attempt.started") != 1 || countType(kept, "attempt.finished") != 1 {
		t.Errorf("started and finished are not covered and must stay: %+v", kept)
	}
}

func TestTimeline_ARealEventForOneAttemptDoesNotHideAnother(t *testing.T) {
	attempts := []models.Attempt{attemptAt("A1", true, false, false, "Claimed"), attemptAt("A2", true, false, false, "Claimed")}
	eventEntries := timelineFromEvents([]models.EventEnvelope{ev("attempt.claimed", "A1")})

	kept := dropCoveredAttemptEntries(eventEntries, timelineFromAttempts(attempts))

	if len(kept) != 1 || attemptIDOf(kept[0]) != "A2" {
		t.Fatalf("only A1's claimed is covered; A2's must stay: %+v", kept)
	}
}

func TestTimeline_EveryOutcomeEventCoversTheSynthesizedFinished(t *testing.T) {
	for _, outcome := range []string{"attempt.succeeded", "attempt.failed", "attempt.abandoned"} {
		attempts := []models.Attempt{attemptAt("A1", false, false, true, "Failed")}
		eventEntries := timelineFromEvents([]models.EventEnvelope{ev(outcome, "A1")})

		if kept := dropCoveredAttemptEntries(eventEntries, timelineFromAttempts(attempts)); len(kept) != 0 {
			t.Errorf("%s should cover the synthesized attempt.finished, %d left", outcome, len(kept))
		}
	}
}

func TestTimeline_AHeartbeatCoversNothing(t *testing.T) {
	attempts := []models.Attempt{attemptAt("A1", true, true, true, "Succeeded")}
	eventEntries := timelineFromEvents([]models.EventEnvelope{ev("attempt.heartbeat", "A1")})

	if kept := dropCoveredAttemptEntries(eventEntries, timelineFromAttempts(attempts)); len(kept) != 3 {
		t.Fatalf("a heartbeat is not a lifecycle step, all 3 synthesized entries must stay, got %d", len(kept))
	}
}

func TestTimeline_ASynthesizedEntryWithNoAttemptIDIsNeverDropped(t *testing.T) {
	entries := []TimelineEntry{{EventType: "attempt.claimed", RunID: "run-1", Source: "ATTEMPT", Detail: map[string]interface{}{"worker_id": "w-1"}}}
	covering := timelineFromEvents([]models.EventEnvelope{ev("attempt.claimed", "A1")})

	if kept := dropCoveredAttemptEntries(covering, entries); len(kept) != 1 {
		t.Fatal("without an attempt id there is nothing to match on, so the entry must be kept")
	}
}

func TestTimeline_TheBuildersCarryTheAttemptID(t *testing.T) {
	synth := timelineFromAttempts([]models.Attempt{attemptAt("A9", true, true, true, "Succeeded")})
	for _, e := range synth {
		if attemptIDOf(e) != "A9" {
			t.Errorf("synthesized %s lacks its attempt id: %+v", e.EventType, e.Detail)
		}
	}

	real := timelineFromEvents([]models.EventEnvelope{ev("attempt.claimed", "A9"), {JobId: "run-1", EventType: "task.created"}})
	if attemptIDOf(real[0]) != "A9" {
		t.Error("a real attempt event must carry its attempt id into the timeline")
	}
	if real[1].Detail != nil {
		t.Error("an event with no attempt id must keep an empty Detail, as before")
	}
}

func TestEnrichAttemptEvents_AddsTheAttemptNumberToRealAttemptEvents(t *testing.T) {
	attempts := []models.Attempt{attemptAt("A1", true, true, true, "Failed"), attemptAt("A2", true, true, true, "Succeeded")}
	attempts[0].AttemptNumber, attempts[1].AttemptNumber = 1, 2
	entries := timelineFromEvents([]models.EventEnvelope{ev("attempt.claimed", "A1"), ev("attempt.started", "A2"), ev("attempt.claimed", "UNKNOWN"), {JobId: "run-1", EventType: "task.created"}})

	enrichAttemptEvents(entries, attempts)

	if entries[0].Detail["attempt_number"] != 1 || entries[1].Detail["attempt_number"] != 2 {
		t.Fatalf("each real attempt event must carry its attempt's number: %v / %v", entries[0].Detail, entries[1].Detail)
	}
	if _, has := entries[2].Detail["attempt_number"]; has {
		t.Fatal("an attempt id the run does not have gets no number")
	}
	if entries[3].Detail != nil {
		t.Fatal("an event that names no attempt must be left exactly as it was")
	}
}

func TestTimelineFromEvents_CarriesTheStreamPosition(t *testing.T) {
	e := models.EventEnvelope{JobId: "run-1", EventType: "task.published", StreamPosition: "1700000000000-3", OccurredAt: time.Now()}
	entries := timelineFromEvents([]models.EventEnvelope{e, {JobId: "run-1", EventType: "task.created"}})

	if entries[0].Detail["stream_position"] != "1700000000000-3" {
		t.Fatalf("the stream position must reach the timeline: %v", entries[0].Detail)
	}
	if entries[1].Detail != nil {
		t.Fatal("an event with no stream position keeps an empty Detail, as before")
	}
	// an attempt event with BOTH keeps both
	both := timelineFromEvents([]models.EventEnvelope{{JobId: "run-1", EventType: "attempt.claimed", AttemptID: "A1", WorkerID: "w", StreamPosition: "9-9"}})
	if both[0].Detail["attempt_id"] != "A1" || both[0].Detail["stream_position"] != "9-9" {
		t.Fatalf("%v", both[0].Detail)
	}
}

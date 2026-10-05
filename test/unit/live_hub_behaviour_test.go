package unit_test

import (
	"fmt"
	"testing"
	"time"

	"MTL_Scheduler_PII_Test/internal/live"
)

// RFC-010 §15 Subscription Scopes

func TestSubscribeFiltered_OnlyReceivesItsOwnSubject(t *testing.T) {
	hub := live.NewHub()
	only := hub.SubscribeFiltered("worker-a")
	all := hub.Subscribe()
	defer hub.Unsubscribe(only)
	defer hub.Unsubscribe(all)

	hub.Publish(live.Event{ID: 1, Type: "worker.online", Subject: "worker-a"})
	hub.Publish(live.Event{ID: 2, Type: "worker.online", Subject: "worker-b"})

	if len(only) != 1 {
		t.Fatalf("filtered client should have received exactly its own subject, got %d events", len(only))
	}
	got := <-only
	if got.Subject != "worker-a" {
		t.Fatalf("filtered client received the wrong subject: %s", got.Subject)
	}
	if len(all) != 2 {
		t.Fatalf("the unfiltered client must still receive everything, got %d", len(all))
	}
}

func TestSubscribe_UnfilteredIsUnchangedBehaviour(t *testing.T) {
	hub := live.NewHub()
	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)

	hub.Publish(live.Event{ID: 1, Type: "task.created", Subject: "job-1"})
	hub.Publish(live.Event{ID: 2, Type: "task.created", Subject: "job-2"})

	if len(ch) != 2 {
		t.Fatalf("a plain Subscribe() must receive every subject, got %d", len(ch))
	}
}

// RFC-010 §19/§20: what happens when a client cannot keep up

func fillBuffer(t *testing.T, hub *live.Hub, ch chan live.Event) {
	t.Helper()
	// Fill to EXACTLY capacity: one more would itself trigger the cut, which
	// is the behaviour under test rather than part of the setup.
	// Distinct subjects so coalescing never suppresses the fill itself -- and
	// unique PER CALL, because the coalescing window is process-wide: reusing
	// "fill-0" across two tests inside 250ms would silently suppress the
	// second test's fill and leave its buffer empty.
	stamp := time.Now().UnixNano()
	for i := 0; i < cap(ch); i++ {
		hub.Publish(live.Event{ID: uint(i + 1), Type: "task.created", Subject: fmt.Sprintf("fill-%d-%d", stamp, i)})
	}
}

func TestSlowClient_InformationalEventIsDropped_ConnectionSurvives(t *testing.T) {
	hub := live.NewHub()
	ch := hub.Subscribe()
	fillBuffer(t, hub, ch) // buffer now full

	before := hub.Stats().InformationalDroppedTotal

	// an informational event has nowhere to go -- it must be dropped, NOT cost
	// the client its connection
	hub.Publish(live.Event{ID: 9001, Type: "task.progress", Subject: "some-job"})

	if got := hub.Stats().InformationalDroppedTotal; got != before+1 {
		t.Fatalf("expected the informational event to be counted as dropped, before=%d after=%d", before, got)
	}
	if hub.Stats().ActiveConnections != 1 {
		t.Fatal("a dropped informational event must NOT disconnect the client")
	}
}

func TestSlowClient_CriticalEventCutsTheConnectionInstead_NeverSilentlyDropped(t *testing.T) {
	hub := live.NewHub()
	ch := hub.Subscribe()
	fillBuffer(t, hub, ch)

	beforeCut := hub.Stats().SlowClientsCutTotal

	// RFC-010 §19: "must not silently drop important changes while still
	// claiming the connection is fully synchronized" -- so this one cuts.
	hub.Publish(live.Event{ID: 9002, Type: "task.failed", Subject: "important-job"})

	if got := hub.Stats().SlowClientsCutTotal; got != beforeCut+1 {
		t.Fatalf("a critical event that cannot be delivered must cut the client, before=%d after=%d", beforeCut, got)
	}
	if hub.Stats().ActiveConnections != 0 {
		t.Fatal("the client should have been disconnected so replay can fill the gap on reconnect")
	}
}

func TestSlowClient_NormalEventAlsoCuts_NotDropped(t *testing.T) {
	hub := live.NewHub()
	ch := hub.Subscribe()
	fillBuffer(t, hub, ch)

	beforeCut := hub.Stats().SlowClientsCutTotal
	hub.Publish(live.Event{ID: 9003, Type: "task.created", Subject: "another-job"})

	if got := hub.Stats().SlowClientsCutTotal; got != beforeCut+1 {
		t.Fatal("a NORMAL event is a fact too -- it must cut rather than be silently dropped")
	}
}

// RFC-010 §19 coalescing

func TestCoalescing_SecondUpdateToSameResourceIsSuppressed(t *testing.T) {
	hub := live.NewHub()
	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)

	subject := fmt.Sprintf("coalesce-target-%d", time.Now().UnixNano())
	hub.Publish(live.Event{ID: 1, Type: "queue.degraded", Subject: subject})
	hub.Publish(live.Event{ID: 2, Type: "queue.degraded", Subject: subject})
	hub.Publish(live.Event{ID: 3, Type: "queue.degraded", Subject: subject})

	if len(ch) != 1 {
		t.Fatalf("3 rapid updates to the same resource should coalesce to 1, got %d", len(ch))
	}
}

func TestCoalescing_DifferentResourcesAreNeverCoalescedTogether(t *testing.T) {
	hub := live.NewHub()
	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)

	stamp := time.Now().UnixNano()
	hub.Publish(live.Event{ID: 1, Type: "queue.degraded", Subject: fmt.Sprintf("q-a-%d", stamp)})
	hub.Publish(live.Event{ID: 2, Type: "queue.degraded", Subject: fmt.Sprintf("q-b-%d", stamp)})

	if len(ch) != 2 {
		t.Fatalf("two DIFFERENT resources must both be delivered, got %d", len(ch))
	}
}

func TestCoalescing_CriticalEventsAreNeverCoalescedAway(t *testing.T) {
	hub := live.NewHub()
	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)

	subject := fmt.Sprintf("critical-target-%d", time.Now().UnixNano())
	hub.Publish(live.Event{ID: 1, Type: "run.lost", Subject: subject})
	hub.Publish(live.Event{ID: 2, Type: "run.lost", Subject: subject})

	if len(ch) != 2 {
		t.Fatalf("critical events must never be suppressed as redundant, got %d of 2", len(ch))
	}
}

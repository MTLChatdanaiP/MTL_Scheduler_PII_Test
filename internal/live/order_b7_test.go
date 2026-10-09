package live

// Batch 7, RFC-010 §20. Priority decides what may be DROPPED when a client's buffer is full; it never reorders delivery. A client
// resumes from the id of the last event it received (RFC-010 §14, §17), so delivering id 120 before id 115 would let a dropped
// connection skip 115 for good. This test pins the order so nobody "improves" priority into reordering.

import "testing"

func TestDelivery_IsInIdOrderWhateverThePriorities(t *testing.T) {
	hub := NewHub()
	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)

	// informational, normal and critical interleaved, with the critical ones published LAST
	types := []string{"attempt.heartbeat", "task.started", "queue.sample", "task.completed", "run.lost", "queue.no_consumer"}
	for i, typ := range types {
		hub.Publish(Event{ID: uint(100 + i), Type: typ, Subject: "s"})
	}

	var got []uint
	for range types {
		select {
		case e := <-ch:
			got = append(got, e.ID)
		default:
			t.Fatalf("only %d of %d events were delivered: %v", len(got), len(types), got)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("delivery order must follow event ids, got %v", got)
		}
	}
}

// Batch 7 (RFC-010 §9): draining and stopping are ordinary state changes. A deploy restart must not page anyone or be treated as a fault.
func TestPriority_DrainingAndStoppedAreNormalNotCritical(t *testing.T) {
	for _, typ := range []string{"component.draining", "worker.draining", "component.stopped", "worker.stopped"} {
		if got := PriorityOf(typ); got != PriorityNormal {
			t.Errorf("%s has priority %v, want PriorityNormal", typ, got)
		}
	}
	for _, typ := range []string{"component.offline", "worker.offline"} {
		if PriorityOf(typ) != PriorityCritical {
			t.Errorf("%s (a crash) must stay critical", typ)
		}
	}
}

func TestDescribe_DrainingIsAHealthChange(t *testing.T) {
	for _, typ := range []string{"worker.draining", "component.draining"} {
		if _, _, change := Describe(typ, "x"); change != ChangeHealthChanged {
			t.Errorf("%s is described as %s, want %s", typ, change, ChangeHealthChanged)
		}
	}
}

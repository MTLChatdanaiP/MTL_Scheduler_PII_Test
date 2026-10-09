package live

// Batch 5 (5B): the live pipeline's numbers. Helper names are prefixed b5.

import (
	"strconv"
	"testing"
	"time"
)

// The counters are process-wide, so earlier tests leave them in an unknown state (some publish without recording, which clamps
// the backlog at 0). These tests set known values first and restore the originals after.
func b5SetCounters(t *testing.T, recorded, published, coalesced int64) {
	t.Helper()
	r, p, c := eventsRecordedTotal.Load(), publishedTotal.Load(), coalescedSuppressed.Load()
	eventsRecordedTotal.Store(recorded)
	publishedTotal.Store(published)
	coalescedSuppressed.Store(coalesced)
	t.Cleanup(func() { eventsRecordedTotal.Store(r); publishedTotal.Store(p); coalescedSuppressed.Store(c) })
}

// reproduced: three identical events inside the window -> recorded +3, published +1, coalesced +2, and the backlog read 2
func TestStats_CoalescedEventsAreNotABacklog(t *testing.T) {
	b5SetCounters(t, 100, 100, 0)
	hub := NewCoalescingHub()

	for i := 0; i < 3; i++ {
		CountRecorded() // what LogEventWith does for every non-noise event
		hub.Publish(Event{ID: uint(900 + i), Type: "task.completed", Subject: "b5-subject-backlog", At: time.Now()})
	}
	got := hub.Stats()

	if got.EventsRecordedTotal != 103 || got.EventsPublishedTotal != 101 || got.CoalescedSuppressedTotal != 2 {
		t.Fatalf("setup: recorded %d published %d coalesced %d", got.EventsRecordedTotal, got.EventsPublishedTotal, got.CoalescedSuppressedTotal)
	}
	if got.LiveChangeBacklog != 0 {
		t.Fatalf("2 suppressed events are not a backlog: it reads %d", got.LiveChangeBacklog)
	}
}

func TestStats_ARealBacklogIsStillReported(t *testing.T) {
	b5SetCounters(t, 100, 100, 0)
	for i := 0; i < 4; i++ {
		CountRecorded() // recorded but never published: that IS a backlog
	}
	if got := NewHub().Stats().LiveChangeBacklog; got != 4 {
		t.Fatalf("an event that was recorded and never published must count: backlog = %d, want 4", got)
	}
}

func b5ResetCoalescing(t *testing.T) {
	t.Helper()
	coalesceMu.Lock()
	lastPublished = map[string]time.Time{}
	lastCoalescePrune = time.Time{}
	coalesceMu.Unlock()
	t.Cleanup(func() {
		coalesceMu.Lock()
		lastPublished = map[string]time.Time{}
		lastCoalescePrune = time.Time{}
		coalesceMu.Unlock()
	})
}

func b5Size() int {
	coalesceMu.Lock()
	defer coalesceMu.Unlock()
	return len(lastPublished)
}

// reproduced: 1000 different subjects left 1001 entries for ever
func TestCoalesce_TheMapDoesNotGrowWithEverySubjectEverySeen(t *testing.T) {
	b5ResetCoalescing(t)
	t0 := time.Now()

	for i := 0; i < 1000; i++ {
		shouldCoalesce("task.started", "b5-run-"+strconv.Itoa(i), t0)
	}
	if b5Size() != 1000 {
		t.Fatalf("setup: expected 1000 entries, got %d", b5Size())
	}

	shouldCoalesce("task.started", "b5-run-new", t0.Add(3*time.Second)) // far outside the window: everything old can be forgotten
	if n := b5Size(); n > 5 {
		t.Fatalf("entries older than the window must be forgotten, %d remain", n)
	}
}

func TestCoalesce_PruningChangesNoDecision(t *testing.T) {
	b5ResetCoalescing(t)
	t0 := time.Now()
	key := func(now time.Time) bool { return shouldCoalesce("task.started", "b5-run-same", now) }

	if key(t0) {
		t.Fatal("the first event is published")
	}
	if !key(t0.Add(100 * time.Millisecond)) {
		t.Fatal("a repeat inside the window is suppressed")
	}
	// a prune runs between these two calls (a second has passed): the entry from t0 is old and may be forgotten
	if key(t0.Add(2 * time.Second)) {
		t.Fatal("an event more than a window after the last published one is published again")
	}
	if !key(t0.Add(2*time.Second + 100*time.Millisecond)) {
		t.Fatal("and its own repeat is suppressed again")
	}
}

func TestCoalesce_PruningNeverForgetsSomethingStillInsideTheWindow(t *testing.T) {
	b5ResetCoalescing(t)
	t0 := time.Now()
	shouldCoalesce("task.started", "b5-run-keep", t0.Add(900*time.Millisecond))
	shouldCoalesce("task.started", "b5-run-other", t0.Add(1100*time.Millisecond)) // triggers a prune at t0+1.1s
	if !shouldCoalesce("task.started", "b5-run-keep", t0.Add(1100*time.Millisecond)) {
		t.Fatal("an entry 200 ms old is inside the 250 ms window and must still suppress")
	}
}

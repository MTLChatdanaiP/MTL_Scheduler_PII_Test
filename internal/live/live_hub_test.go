package live

import (
	"sync"
	"testing"
	"time"
)

func ev(id uint, typ string) Event {
	return Event{ID: id, Type: typ, Subject: "s", At: time.Now().UTC()}
}

// The counters are process-wide, so every test measures a DELTA between two
// snapshots instead of asserting an absolute value. Active connections are
// per hub, so tests use their own NewHub().

func TestPublishReachesEverySubscriber(t *testing.T) {
	h := NewHub()
	a, b := h.Subscribe(), h.Subscribe()
	defer h.Unsubscribe(a)
	defer h.Unsubscribe(b)

	h.Publish(ev(1, "task.created"))

	for name, ch := range map[string]chan Event{"a": a, "b": b} {
		select {
		case got := <-ch:
			if got.ID != 1 {
				t.Errorf("subscriber %s got id %d, want 1", name, got.ID)
			}
		case <-time.After(time.Second):
			t.Errorf("subscriber %s did not receive the event", name)
		}
	}
}

func TestUnsubscribeClosesTheChannelAndIsSafeToRepeat(t *testing.T) {
	h := NewHub()
	ch := h.Subscribe()
	before := h.Stats()

	h.Unsubscribe(ch)
	h.Unsubscribe(ch) // must not panic (double close) or count twice

	if _, open := <-ch; open {
		t.Error("channel should be closed after Unsubscribe")
	}

	after := h.Stats()
	if got := after.DisconnectionsTotal - before.DisconnectionsTotal; got != 1 {
		t.Errorf("disconnect counted %d times, want exactly 1", got)
	}
	if after.ActiveConnections != 0 {
		t.Errorf("active connections = %d, want 0", after.ActiveConnections)
	}
}

func TestSlowSubscriberIsDisconnectedAndCounted(t *testing.T) {
	h := NewHub()
	slow := h.Subscribe() // never reads
	healthy := h.Subscribe()
	defer h.Unsubscribe(healthy)

	before := h.Stats()

	// The buffer holds bufferSize events; one more cannot fit.
	for i := 0; i < bufferSize+1; i++ {
		h.Publish(ev(uint(i+1), "task.created"))
		<-healthy // keep the healthy subscriber drained
	}

	after := h.Stats()
	if got := after.SlowClientsCutTotal - before.SlowClientsCutTotal; got != 1 {
		t.Errorf("slow clients cut = %d, want 1", got)
	}
	if after.ActiveConnections != 1 {
		t.Errorf("active connections = %d, want 1 (only the healthy client)", after.ActiveConnections)
	}

	// The slow client keeps what it had buffered, then its channel is closed.
	received := 0
	for range slow {
		received++
	}
	if received != bufferSize {
		t.Errorf("slow client received %d events before the cut, want %d", received, bufferSize)
	}

	// Cutting one client must not affect the others.
	h.Publish(ev(9999, "alert.opened"))
	select {
	case got := <-healthy:
		if got.ID != 9999 {
			t.Errorf("healthy client got id %d, want 9999", got.ID)
		}
	case <-time.After(time.Second):
		t.Error("healthy client stopped receiving after another client was cut")
	}
}

func TestStatsCountPublishesAndDeliveries(t *testing.T) {
	h := NewHub()
	a, b := h.Subscribe(), h.Subscribe()
	defer h.Unsubscribe(a)
	defer h.Unsubscribe(b)

	before := h.Stats()

	h.Publish(ev(1, "task.created"))
	h.Publish(ev(2, "task.queued"))

	after := h.Stats()

	if got := after.EventsPublishedTotal - before.EventsPublishedTotal; got != 2 {
		t.Errorf("published = %d, want 2", got)
	}
	if got := after.EventsDeliveredTotal - before.EventsDeliveredTotal; got != 4 {
		t.Errorf("delivered = %d, want 4 (2 events x 2 clients)", got)
	}
	if after.LastPublishedAt == nil {
		t.Error("LastPublishedAt should be set after a publish")
	}
	if after.MaxBufferDepth < 2 {
		t.Errorf("max buffer depth = %d, want at least 2 (two unread events per client)", after.MaxBufferDepth)
	}
	if after.BufferCapacity != bufferSize {
		t.Errorf("buffer capacity = %d, want %d", after.BufferCapacity, bufferSize)
	}
}

func TestConnectCounterIncrements(t *testing.T) {
	h := NewHub()
	before := h.Stats()
	ch := h.Subscribe()
	defer h.Unsubscribe(ch)

	if got := h.Stats().ConnectionsTotal - before.ConnectionsTotal; got != 1 {
		t.Errorf("connections counted %d, want 1", got)
	}
}

func TestReplayResyncAndHeartbeatCounters(t *testing.T) {
	before := GlobalHub.Stats()

	CountReplay(7)
	CountReplay(3)
	CountResync()
	MarkHeartbeat()

	after := GlobalHub.Stats()

	if got := after.ReplaysTotal - before.ReplaysTotal; got != 2 {
		t.Errorf("replays = %d, want 2", got)
	}
	if got := after.EventsReplayedTotal - before.EventsReplayedTotal; got != 10 {
		t.Errorf("events replayed = %d, want 10", got)
	}
	if got := after.ResyncsTotal - before.ResyncsTotal; got != 1 {
		t.Errorf("resyncs = %d, want 1", got)
	}
	if after.LastHeartbeatAt == nil {
		t.Error("LastHeartbeatAt should be set after MarkHeartbeat")
	}
}

func TestDeliveryLagTracksLastAndMax(t *testing.T) {
	ObserveDeliveryLag(40 * time.Millisecond)
	ObserveDeliveryLag(10 * time.Millisecond)

	s := GlobalHub.Stats()
	if s.LastDeliveryLagMs != 10 {
		t.Errorf("last lag = %d, want 10", s.LastDeliveryLagMs)
	}
	if s.MaxDeliveryLagMs < 40 {
		t.Errorf("max lag = %d, want at least 40", s.MaxDeliveryLagMs)
	}

	ObserveDeliveryLag(-5 * time.Millisecond) // clock skew must not produce a negative lag
	if got := GlobalHub.Stats().LastDeliveryLagMs; got != 0 {
		t.Errorf("negative lag should clamp to 0, got %d", got)
	}
}

func TestIsNoise(t *testing.T) {
	if !IsNoise("task.progress") {
		t.Error("task.progress must be noise")
	}
	for _, typ := range []string{"task.created", "alert.opened", "pii.detected", ""} {
		if IsNoise(typ) {
			t.Errorf("%q must not be noise", typ)
		}
	}
}

func TestConcurrentUseIsSafe(t *testing.T) {
	h := NewHub()
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch := h.Subscribe()
			for j := 0; j < 50; j++ {
				h.Publish(ev(uint(j), "task.created"))
				select {
				case <-ch:
				default:
				}
			}
			h.Unsubscribe(ch)
		}()
	}
	wg.Wait()

	if got := h.Stats().ActiveConnections; got != 0 {
		t.Errorf("active connections = %d after everyone unsubscribed, want 0", got)
	}
}

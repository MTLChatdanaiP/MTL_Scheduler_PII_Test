package live

import (
	"testing"
	"time"
)

// RFC-010 §19 coalescing is a property of the PRODUCTION hub, not of every hub.
//
// WHY: coalescing once applied to every hub, so a plain NewHub() silently dropped
// the second and later of any identical (type, subject) publish within 250ms. The
// existing TestSlowSubscriberIsDisconnectedAndCounted publishes 257 identical
// events and then blocks on a receive that never arrives, so the whole package
// hung until the 10-minute test timeout.

func TestPlainHubDeliversEveryEventItIsGiven(t *testing.T) {
	h := NewHub()
	ch := h.Subscribe()
	defer h.Unsubscribe(ch)

	for i := 0; i < 5; i++ {
		h.Publish(Event{ID: uint(i + 1), Type: "queue.degraded", Subject: "same-subject", At: time.Now()})
	}

	if len(ch) != 5 {
		t.Fatalf("a plain hub must not suppress anything, delivered %d of 5", len(ch))
	}
}

func TestCoalescingHubSuppressesARedundantRepublish(t *testing.T) {
	h := NewCoalescingHub()
	ch := h.Subscribe()
	defer h.Unsubscribe(ch)

	subject := "coalescing-hub-" + time.Now().Format("150405.000000000")
	for i := 0; i < 5; i++ {
		h.Publish(Event{ID: uint(i + 1), Type: "queue.degraded", Subject: subject, At: time.Now()})
	}

	if len(ch) != 1 {
		t.Fatalf("a coalescing hub should collapse 5 identical publishes into 1, delivered %d", len(ch))
	}
}

func TestGlobalHubIsTheCoalescingOne(t *testing.T) {
	// The production feed is the one place coalescing is wanted. If this ever
	// reverts to a plain hub, RFC-010 §19's coalescing silently stops.
	if !GlobalHub.coalesce {
		t.Fatal("GlobalHub must be a coalescing hub")
	}
}

func TestCoalescingNeverSuppressesACriticalEvent(t *testing.T) {
	h := NewCoalescingHub()
	ch := h.Subscribe()
	defer h.Unsubscribe(ch)

	subject := "critical-" + time.Now().Format("150405.000000000")
	h.Publish(Event{ID: 1, Type: "run.lost", Subject: subject, At: time.Now()})
	h.Publish(Event{ID: 2, Type: "run.lost", Subject: subject, At: time.Now()})

	if len(ch) != 2 {
		t.Fatalf("critical events are never coalesced, delivered %d of 2", len(ch))
	}
}

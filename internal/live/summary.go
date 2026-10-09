package live

import (
	"sync"
	"time"
)

// RFC-010 §7 PLATFORM_SUMMARY.
//
// "Something that feeds the platform overview just changed." One synthetic event says so, instead of every client re-deriving it
// from the full firehose. It is NOT a recorded fact: it has id 0, is never written to event_envelopes and is never replayed. A client
// that reconnects gets one fresh summary frame from the live handler instead (see handlers.GetLive).
//
// TRAILING edge on purpose. The first change in a burst arms a timer; when it fires, ONE summary goes out, after the burst's last
// change. A leading-edge window (send now, suppress for a second) would swallow the final change of a burst and leave the overview
// showing the state from before it.

// summaryDelay is how long after the first change of a burst the summary is sent.
const summaryDelay = time.Second

// summarySubject is the fixed subject of the summary event: there is one platform.
const summarySubject = "platform"

// summaryNotifier turns many NotifySummaryChanged calls into one PublishSummary per delay window.
type summaryNotifier struct {
	mu      sync.Mutex
	pending bool
	delay   time.Duration
	hub     *Hub
	now     func() time.Time
}

func newSummaryNotifier(hub *Hub, delay time.Duration) *summaryNotifier {
	return &summaryNotifier{hub: hub, delay: delay, now: time.Now}
}

// notify records that something changed. The first call of a burst arms the timer; the rest are absorbed by it.
func (n *summaryNotifier) notify() {
	n.mu.Lock()
	if n.pending {
		n.mu.Unlock()
		return
	}
	n.pending = true
	n.mu.Unlock()

	time.AfterFunc(n.delay, n.fire)
}

func (n *summaryNotifier) fire() {
	// Clear BEFORE publishing: a change that lands while we publish arms the next window instead of being absorbed by this one.
	n.mu.Lock()
	n.pending = false
	n.mu.Unlock()

	n.hub.PublishSummary(Event{Type: TypePlatformSummary, Subject: summarySubject, At: n.now().UTC()})
}

var globalSummary = newSummaryNotifier(GlobalHub, summaryDelay)

// NotifySummaryChanged is called after every durable, non-noise event. It is cheap and safe to call from any goroutine.
func NotifySummaryChanged() { globalSummary.notify() }

// SummaryEvent builds a platform.summary event stamped now. The live handler sends one after a replay that delivered rows.
func SummaryEvent() Event {
	return Event{Type: TypePlatformSummary, Subject: summarySubject, At: time.Now().UTC()}
}

package live

import (
	"sync"
	"sync/atomic"
	"time"
)

// bufferSize is how many events one client may fall behind before it is
// disconnected (see Publish).
const bufferSize = 256

// Event is a lightweight "something changed" notification. ID is the
// event_envelopes.id -- the same cursor space as live.watermark.
//
// RFC-010 §10: ChainID and AttemptID let a client subscribe to one execution chain (§15) and let the envelope name the attempt.
// Severity is carried only so the hub can rank an alert.opened by how serious the alert is (§20); it is never sent to a browser.
// There is deliberately no payload field: nothing in this system streams a payload (§12), so payload_mode is always NONE.
type Event struct {
	ID        uint      `json:"id"`
	Type      string    `json:"type"`
	Subject   string    `json:"subject"`
	At        time.Time `json:"at"`
	ChainID   string    `json:"execution_chain_id,omitempty"`
	AttemptID string    `json:"attempt_id,omitempty"`
	Severity  string    `json:"-"`
}

// RFC-010: heartbeat-style noise must not be streamed.
func IsNoise(eventType string) bool {
	// RFC-010 §19/§20: both are high-frequency, low-investigative-value while an
	// attempt is in progress -- durably recorded, but not worth a live push.
	return eventType == "task.progress" || eventType == "attempt.heartbeat"
}

// ---------------------------------------------------------------------------
// RFC-010 §24: the live pipeline monitors itself.
//
// Plain process-wide counters kept in memory (they reset when the backend
// restarts). There are deliberately NO labels: resource ids must never become
// metric dimensions.
// ---------------------------------------------------------------------------

var (
	connectsTotal      atomic.Int64
	disconnectsTotal   atomic.Int64
	publishedTotal     atomic.Int64
	deliveredTotal     atomic.Int64 // events placed into a client's buffer
	slowCutTotal       atomic.Int64 // clients disconnected for falling behind
	replaysTotal       atomic.Int64 // reconnects that were served a replay
	replayedTotal      atomic.Int64 // events sent by those replays
	resyncsTotal       atomic.Int64 // "resync" frames sent (gap too large to replay)
	maxBufferDepth     atomic.Int64 // deepest client buffer ever observed
	lastDeliveryLagMs  atomic.Int64
	maxDeliveryLagMs   atomic.Int64
	lastPublishNanos   atomic.Int64
	lastHeartbeatNanos atomic.Int64

	// RFC-005 §21 "monitoring the monitoring and live pipelines"
	eventsRecordedTotal     atomic.Int64 // non-noise events durably recorded (counted BEFORE projection + publish)
	eventWriteFailuresTotal atomic.Int64 // event rows that failed to write, so nothing was published for them
	lastPublishLagMs        atomic.Int64
	maxPublishLagMs         atomic.Int64

	// RFC-010 §19/§20
	informationalDropped atomic.Int64 // low-value events dropped instead of cutting a slow client
	coalescedSuppressed  atomic.Int64 // redundant same-resource publishes suppressed

	// RFC-010 §19: subscriptions refused because the caller was over its connection cap
	rejectedSubscriptions atomic.Int64
)

// CountRejectedSubscription records one live connection refused by the per-caller cap.
func CountRejectedSubscription() { rejectedSubscriptions.Add(1) }

// Stats is the JSON shape exposed under "live_pipeline" on GET /monitoring/health.
type Stats struct {
	ActiveConnections    int        `json:"active_connections"`
	ConnectionsTotal     int64      `json:"connections_total"`
	DisconnectionsTotal  int64      `json:"disconnections_total"`
	EventsPublishedTotal int64      `json:"events_published_total"`
	EventsDeliveredTotal int64      `json:"events_delivered_total"`
	SlowClientsCutTotal  int64      `json:"slow_clients_disconnected_total"`
	ReplaysTotal         int64      `json:"replays_total"`
	EventsReplayedTotal  int64      `json:"events_replayed_total"`
	ResyncsTotal         int64      `json:"resyncs_total"`
	MaxBufferDepth       int64      `json:"max_client_buffer_depth"`
	BufferCapacity       int        `json:"client_buffer_capacity"`
	LastDeliveryLagMs    int64      `json:"last_delivery_lag_ms"`
	MaxDeliveryLagMs     int64      `json:"max_delivery_lag_ms"`
	LastPublishedAt      *time.Time `json:"last_published_at,omitempty"`
	LastHeartbeatAt      *time.Time `json:"last_heartbeat_at,omitempty"`

	// RFC-005 §21
	EventsRecordedTotal     int64 `json:"events_recorded_total"`
	LiveChangeBacklog       int64 `json:"live_change_backlog"`
	EventWriteFailuresTotal int64 `json:"event_write_failures_total"`
	LastPublishLagMs        int64 `json:"last_publish_lag_ms"`
	MaxPublishLagMs         int64 `json:"max_publish_lag_ms"`

	// RFC-010 §19/§20
	InformationalDroppedTotal int64 `json:"informational_dropped_total"`
	CoalescedSuppressedTotal  int64 `json:"coalesced_suppressed_total"`

	// RFC-010 §19 subscription limits
	RejectedSubscriptionsTotal int64 `json:"rejected_subscriptions_total"`
}

// storeMax raises a to v if v is larger, without a lock.
func storeMax(a *atomic.Int64, v int64) {
	for {
		cur := a.Load()
		if v <= cur || a.CompareAndSwap(cur, v) {
			return
		}
	}
}

func nanosToTime(n int64) *time.Time {
	if n == 0 {
		return nil
	}
	t := time.Unix(0, n).UTC()
	return &t
}

// Backlog is the RFC-005 §21 "live-change outbox backlog": events durably recorded but not yet handed to the hub.
// Publishing is synchronous right after the write, so this stays at about 0; a value that grows means
// something recorded an event and never published it.
// A coalesced event was recorded and deliberately NOT published (a second signal for the same thing within the window), so it
// counts as dealt with here: Stats passes published + coalesced. Without that, every suppressed event looked like a backlog.
func Backlog(recorded, published int64) int64 {
	if recorded < published {
		return 0
	}
	return recorded - published
}

// CountRecorded is called once an event row has been written, before the projection update and the publish.
func CountRecorded() { eventsRecordedTotal.Add(1) }

// CountEventWriteFailure is called when an event row could not be written.
func CountEventWriteFailure() { eventWriteFailuresTotal.Add(1) }

// ObservePublishLag records how long after a fact occurred it was handed to the hub
// (RFC-005 §21 "live publisher lag"). A negative value (clock skew) is clamped to 0.
func ObservePublishLag(d time.Duration) {
	ms := d.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	lastPublishLagMs.Store(ms)
	storeMax(&maxPublishLagMs, ms)
}

// ObserveDeliveryLag records how long an event took from being created to
// being written to a client. A negative value (clock skew) is clamped to 0.
func ObserveDeliveryLag(d time.Duration) {
	ms := d.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	lastDeliveryLagMs.Store(ms)
	storeMax(&maxDeliveryLagMs, ms)
}

// CountReplay records one reconnect that was served a replay of n events.
func CountReplay(n int) {
	replaysTotal.Add(1)
	replayedTotal.Add(int64(n))
}

// CountResync records one "resync" frame sent to a client.
func CountResync() {
	resyncsTotal.Add(1)
}

// MarkHeartbeat records that a keepalive ping was just sent to a client.
func MarkHeartbeat() {
	lastHeartbeatNanos.Store(time.Now().UnixNano())
}

// ---------------------------------------------------------------------------
// The hub
// ---------------------------------------------------------------------------

// A minimal in-process pub/sub. One backend instance, one process -- no
// Redis needed for this. If this project ever runs multiple backend
// instances behind a load balancer, THIS is the file that gets replaced with
// Redis Pub/Sub (which RFC-010 explicitly names as one valid transport) --
// nothing else in the live package needs to change, since callers only ever
// see Publish/Subscribe/Unsubscribe.
// subState is the per-client state the hub keeps alongside each channel.
type subState struct {
	// match narrows this client to the scopes it subscribed to (RFC-010 §15):
	// one resource (worker:{id}), a family (queues), a set of them, or an
	// execution chain. nil means no narrowing at all -- the default, and exactly
	// the behaviour every plain Subscribe() caller always had.
	match func(Event) bool

	// summary is true only for a client that asked for the platform.summary
	// scope. The synthetic platform.summary event (RFC-010 §7) is delivered to
	// these clients and to nobody else, so a plain Subscribe() never sees it.
	summary bool
}

type Hub struct {
	mu          sync.Mutex
	subscribers map[chan Event]*subState

	// coalesce turns on RFC-010 §19 publish-side coalescing for THIS hub.
	//
	// It is a property of the hub, not of the package, on purpose. Coalescing
	// suppresses a second publish of the same type and subject inside a short
	// window, which is right for the production feed (every event only means
	// "refetch") but wrong as a default: a plain NewHub() is expected to deliver
	// exactly what it is given, and a test that publishes identical events through
	// one (internal/live/live_hub_test.go publishes 257 of the same type and
	// subject) hung forever once coalescing applied to every hub.
	coalesce bool
}

// NewHub returns a hub that delivers every event it is given.
func NewHub() *Hub {
	return &Hub{subscribers: make(map[chan Event]*subState)}
}

// NewCoalescingHub returns a hub that also suppresses a redundant republish of the
// same resource inside the coalesce window. The production GlobalHub is one.
func NewCoalescingHub() *Hub {
	h := NewHub()
	h.coalesce = true
	return h
}

var GlobalHub = NewCoalescingHub()

// Snapshot returns the current pipeline statistics for the global hub.
func Snapshot() Stats {
	return GlobalHub.Stats()
}

// Stats reads this hub's live connection count plus the process-wide counters.
func (h *Hub) Stats() Stats {
	h.mu.Lock()
	active := len(h.subscribers)
	h.mu.Unlock()

	return Stats{
		ActiveConnections:    active,
		ConnectionsTotal:     connectsTotal.Load(),
		DisconnectionsTotal:  disconnectsTotal.Load(),
		EventsPublishedTotal: publishedTotal.Load(),
		EventsDeliveredTotal: deliveredTotal.Load(),
		SlowClientsCutTotal:  slowCutTotal.Load(),
		ReplaysTotal:         replaysTotal.Load(),
		EventsReplayedTotal:  replayedTotal.Load(),
		ResyncsTotal:         resyncsTotal.Load(),
		MaxBufferDepth:       maxBufferDepth.Load(),
		BufferCapacity:       bufferSize,
		LastDeliveryLagMs:    lastDeliveryLagMs.Load(),
		MaxDeliveryLagMs:     maxDeliveryLagMs.Load(),
		LastPublishedAt:      nanosToTime(lastPublishNanos.Load()),
		LastHeartbeatAt:      nanosToTime(lastHeartbeatNanos.Load()),

		EventsRecordedTotal:     eventsRecordedTotal.Load(),
		LiveChangeBacklog:       Backlog(eventsRecordedTotal.Load(), publishedTotal.Load()+coalescedSuppressed.Load()),
		EventWriteFailuresTotal: eventWriteFailuresTotal.Load(),
		LastPublishLagMs:        lastPublishLagMs.Load(),
		MaxPublishLagMs:         maxPublishLagMs.Load(),

		InformationalDroppedTotal: informationalDropped.Load(),
		CoalescedSuppressedTotal:  coalescedSuppressed.Load(),

		RejectedSubscriptionsTotal: rejectedSubscriptions.Load(),
	}
}

// Subscribe returns a channel that receives every future Publish call.
func (h *Hub) Subscribe() chan Event {
	return h.SubscribeFiltered("")
}

// SubscribeFiltered is Subscribe narrowed to a single resource (RFC-010 §15).
// An empty subject means no narrowing, so Subscribe() is exactly this with the
// filter switched off -- every existing caller keeps today's behaviour.
func (h *Hub) SubscribeFiltered(subject string) chan Event {
	if subject == "" {
		return h.SubscribeMatching(nil, false)
	}
	return h.SubscribeMatching(func(e Event) bool { return e.Subject == subject }, false)
}

// SubscribeMatching is the general form (RFC-010 §15): the client receives an event only if match returns true for it. A nil match
// receives everything. wantsSummary also opts the client in to the synthetic platform.summary event.
func (h *Hub) SubscribeMatching(match func(Event) bool, wantsSummary bool) chan Event {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Buffered so one slow client does not block the publisher. While a
	// reconnecting client is being replayed, live events queue up here.
	ch := make(chan Event, bufferSize)
	h.subscribers[ch] = &subState{match: match, summary: wantsSummary}
	connectsTotal.Add(1)
	return ch
}

func (h *Hub) Unsubscribe(ch chan Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Publish may already have removed and closed it. Count a disconnect only
	// when this call actually removes the subscriber, so each one counts once.
	if _, ok := h.subscribers[ch]; ok {
		delete(h.subscribers, ch)
		close(ch)
		disconnectsTotal.Add(1)
	}
}

func (h *Hub) Publish(event Event) {
	// RFC-010 §19 coalescing: a second "this resource changed" signal that
	// arrives while the first is still fresh tells the browser nothing new,
	// because the browser's response to either is the same refetch. Checked
	// before the lock so a suppressed publish costs almost nothing. Critical
	// events are never suppressed (see shouldCoalesce).
	if h.coalesce && shouldCoalesce(event.Type, event.Subject, time.Now()) {
		coalescedSuppressed.Add(1)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	publishedTotal.Add(1)
	lastPublishNanos.Store(time.Now().UnixNano())

	priority := PriorityOfEvent(event)

	for ch, st := range h.subscribers {
		// RFC-010 §15: a client only gets what it subscribed to. nil match (the default) matches everything.
		if st.match != nil && !st.match(event) {
			continue
		}

		select {
		case ch <- event:
			deliveredTotal.Add(1)
			storeMax(&maxBufferDepth, int64(len(ch)))
		default:
			// This client cannot keep up.
			//
			// RFC-010 §19/§20: what happens next depends on what was lost.
			// An INFORMATIONAL event is a sample -- the next one supersedes
			// it, so dropping it loses nothing an operator could act on, and
			// cutting a connection over one would be worse than the problem.
			// Anything NORMAL or CRITICAL is a fact: dropping it silently
			// would leave a gap the client cannot detect while still being
			// told it is fully synchronized, which §19 explicitly forbids --
			// so the client is cut instead, and replay fills the gap on
			// reconnect.
			if priority == PriorityInformational {
				informationalDropped.Add(1)
				continue
			}

			delete(h.subscribers, ch)
			close(ch)
			slowCutTotal.Add(1)
			disconnectsTotal.Add(1)
		}
	}
}

// PublishSummary delivers the synthetic platform.summary event (RFC-010 §7) to the clients that subscribed to it.
//
// It is not a recorded fact, so it is deliberately kept out of the books: it does not count as "published" (which would hide a real
// live-change backlog, see Backlog), it is never coalesced and it never cuts a slow client. It is an INFORMATIONAL signal -- if a
// client's buffer is full it is dropped, because the next summary (or the client's own resync) supersedes it.
func (h *Hub) PublishSummary(event Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for ch, st := range h.subscribers {
		if !st.summary {
			continue
		}
		select {
		case ch <- event:
			deliveredTotal.Add(1)
		default:
			informationalDropped.Add(1)
		}
	}
}

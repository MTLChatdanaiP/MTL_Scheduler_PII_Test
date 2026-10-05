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
type Event struct {
	ID      uint        `json:"id"`
	Type    string      `json:"type"`
	Subject string      `json:"subject"`
	At      time.Time   `json:"at"`
	Payload interface{} `json:"payload,omitempty"`
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
)

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
// Today that is only the optional subject filter; it exists as a struct so
// future per-client settings do not need another parallel map.
type subState struct {
	// subjectFilter narrows this client to ONE resource (RFC-010 §15
	// resource-instance scopes, e.g. worker:{worker_id}). Empty means no
	// narrowing at all -- the default, and exactly today's behaviour.
	subjectFilter string
}

type Hub struct {
	mu          sync.Mutex
	subscribers map[chan Event]*subState
}

func NewHub() *Hub {
	return &Hub{subscribers: make(map[chan Event]*subState)}
}

var GlobalHub = NewHub()

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
		LiveChangeBacklog:       Backlog(eventsRecordedTotal.Load(), publishedTotal.Load()),
		EventWriteFailuresTotal: eventWriteFailuresTotal.Load(),
		LastPublishLagMs:        lastPublishLagMs.Load(),
		MaxPublishLagMs:         maxPublishLagMs.Load(),

		InformationalDroppedTotal: informationalDropped.Load(),
		CoalescedSuppressedTotal:  coalescedSuppressed.Load(),
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
	h.mu.Lock()
	defer h.mu.Unlock()

	// Buffered so one slow client does not block the publisher. While a
	// reconnecting client is being replayed, live events queue up here.
	ch := make(chan Event, bufferSize)
	h.subscribers[ch] = &subState{subjectFilter: subject}
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
	if shouldCoalesce(event.Type, event.Subject, time.Now()) {
		coalescedSuppressed.Add(1)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	publishedTotal.Add(1)
	lastPublishNanos.Store(time.Now().UnixNano())

	priority := PriorityOf(event.Type)

	for ch, st := range h.subscribers {
		// RFC-010 §15: a client that asked for one resource only gets that
		// resource. Empty filter (the default) matches everything.
		if st.subjectFilter != "" && st.subjectFilter != event.Subject {
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

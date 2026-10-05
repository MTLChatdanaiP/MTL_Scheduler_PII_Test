package live

import (
	"strings"
	"sync"
	"time"
)

// RFC-010 §20 Event Priority.
//
// Priority exists for ONE purpose: to decide what happens when a client cannot
// keep up. It never reorders anything in the normal case -- events are still
// delivered in publish order while the buffer has room.
type EventPriority int

const (
	// PriorityInformational is a high-frequency sample. Losing one tells the
	// operator nothing they cannot get from the next one, so it is the only
	// tier that may be dropped silently.
	PriorityInformational EventPriority = iota
	// PriorityNormal is an ordinary state change.
	PriorityNormal
	// PriorityCritical is a terminal or health-degrading change -- the RFC's
	// "critical operational change" list. Never dropped silently.
	PriorityCritical
)

// criticalTypes are RFC-010 §20's "critical operational change" list, as the
// event types this system actually emits.
var criticalTypes = map[string]bool{
	"task.failed":                  true, // RUN_FAILED
	"run.lost":                     true, // RUN_LOST
	"worker.offline":               true, // WORKER_OFFLINE
	"queue.no_consumer":            true, // QUEUE_NO_CONSUMER
	"alert.opened":                 true, // ALERT_OPENED
	"schedule.missed":              true,
	"monitoring.degraded":          true, // MONITORING_DEGRADED
	"component.offline":            true,
	"pii.policy_reload_failed":     true,
	"pii.policy_validation_failed": true,
}

// informationalPrefixes are RFC-010 §20's "high-frequency informational
// signal" tier: samples, not facts worth protecting.
var informationalPrefixes = []string{
	"task.progress",
	"attempt.heartbeat",
	"queue.pending_count",
	"queue.oldest_age",
	"queue.consumer_count",
	"queue.read",
	"worker.heartbeat_age",
	"worker.capacity_used",
}

// PriorityOf takes an event type and returns which tier it belongs to, by
// checking it against the critical list first and the informational list
// second. Anything unrecognised is treated as Normal -- the safe default,
// since Normal is never dropped silently.
func PriorityOf(eventType string) EventPriority {
	if criticalTypes[eventType] {
		return PriorityCritical
	}
	for _, p := range informationalPrefixes {
		if eventType == p || strings.HasPrefix(eventType, p+".") {
			return PriorityInformational
		}
	}
	return PriorityNormal
}

// ---------------------------------------------------------------------------
// RFC-010 §19: coalescing
// ---------------------------------------------------------------------------

// coalesceWindow is how close together two updates to the SAME resource must
// be for the second to be treated as superseding the first.
//
// This is safe in THIS architecture specifically because no live event carries
// a payload that gets applied: every event only tells the browser "refetch".
// Two refetch signals for the same resource 200ms apart produce the same
// screen as one, so suppressing the first is lossless. It would NOT be safe in
// a system that streamed deltas the client applies in order.
var coalesceWindow = 250 * time.Millisecond

var (
	coalesceMu     sync.Mutex
	lastPublished  = map[string]time.Time{} // "type|subject" -> when it last went out
	coalescedTotal int64
)

// shouldCoalesce takes an event type and subject and returns true if an
// identical resource update was already published within coalesceWindow --
// meaning this one can be suppressed without the operator losing anything.
// Critical events are never coalesced, however frequent they are.
func shouldCoalesce(eventType, subject string, now time.Time) bool {
	if PriorityOf(eventType) == PriorityCritical {
		return false
	}

	key := eventType + "|" + subject

	coalesceMu.Lock()
	defer coalesceMu.Unlock()

	last, seen := lastPublished[key]
	if seen && now.Sub(last) < coalesceWindow {
		coalescedTotal++
		return true
	}
	lastPublished[key] = now
	return false
}

// CoalescedTotal reports how many publishes were suppressed as redundant.
func CoalescedTotal() int64 {
	coalesceMu.Lock()
	defer coalesceMu.Unlock()
	return coalescedTotal
}

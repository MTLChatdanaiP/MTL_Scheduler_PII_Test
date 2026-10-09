package live

import (
	"strconv"
	"strings"
	"time"
)

// RFC-010 §10 Live Update Envelope and §11 Change Types.
//
// The envelope is ADDITIVE: it keeps the four keys every existing client reads (id, type, subject, at) and adds the RFC's fields next
// to them, so an old dashboard against a new backend (and the reverse) keeps working.

// SchemaVersion is the envelope's version. Bump it only for a change an old client cannot ignore.
const SchemaVersion = 1

// Resource types (RFC-010 §7).
const (
	ResourcePlatformSummary        = "PLATFORM_SUMMARY"
	ResourceRun                    = "RUN"
	ResourceExecutionChain         = "EXECUTION_CHAIN"
	ResourceAttempt                = "ATTEMPT"
	ResourceQueue                  = "QUEUE"
	ResourceWorker                 = "WORKER"
	ResourceSchedule               = "SCHEDULE"
	ResourceAlert                  = "ALERT"
	ResourcePIIFindingSummary      = "PII_FINDING_SUMMARY"
	ResourcePIIPolicyActivated     = "PII_POLICY_ACTIVATED"
	ResourcePIIPolicyReloadFailed  = "PII_POLICY_RELOAD_FAILED"
	ResourcePIIPolicyDriftDetected = "PII_POLICY_DRIFT_DETECTED"
	ResourceMonitoringHealth       = "MONITORING_HEALTH"
	ResourceComponent              = "COMPONENT"
	ResourceUnknown                = "UNKNOWN"
)

// Change types (RFC-010 §11). UPSERT and DELETE are never produced: they describe a payload the client applies, and this system never
// streams one (payload_mode is always NONE), so every change is either "the resource's health changed" or "refetch it".
const (
	ChangeInvalidate     = "INVALIDATE"
	ChangeHealthChanged  = "HEALTH_CHANGED"
	ChangeResyncRequired = "RESYNC_REQUIRED"
)

// PayloadModeNone says the envelope carries no payload and the client must fetch the resource from the snapshot API.
const PayloadModeNone = "NONE"

// TypePlatformSummary is the synthetic aggregate event (RFC-010 §7, see summary.go).
const TypePlatformSummary = "platform.summary"

// freshLagMs is how far behind the moment a fact happened an event may be published and still count as FRESH.
const freshLagMs = 5000

// Describe takes an event type and its subject and returns the resource class it belongs to, that resource's id and how it changed.
// An unrecognised type becomes UNKNOWN + INVALIDATE: the client is told to refetch, never to guess.
func Describe(eventType, subject string) (resourceType, resourceID, changeType string) {
	resourceID = subject
	changeType = ChangeInvalidate

	switch {
	case eventType == TypePlatformSummary:
		resourceType = ResourcePlatformSummary
	case strings.HasPrefix(eventType, "task."), strings.HasPrefix(eventType, "run."):
		resourceType = ResourceRun
	case strings.HasPrefix(eventType, "attempt."):
		resourceType = ResourceAttempt
	case strings.HasPrefix(eventType, "queue."):
		resourceType = ResourceQueue
		switch eventType {
		case "queue.degraded", "queue.healthy", "queue.no_consumer", "queue.consumer_restored":
			changeType = ChangeHealthChanged
		}
	case strings.HasPrefix(eventType, "worker."):
		resourceType = ResourceWorker
		switch eventType {
		case "worker.online", "worker.degraded", "worker.offline", "worker.stopped", "worker.draining":
			changeType = ChangeHealthChanged
		}
	case strings.HasPrefix(eventType, "schedule."):
		resourceType = ResourceSchedule
	case strings.HasPrefix(eventType, "alert."):
		resourceType = ResourceAlert
	case eventType == "pii.policy_activated":
		resourceType = ResourcePIIPolicyActivated
	case eventType == "pii.policy_reload_failed":
		resourceType = ResourcePIIPolicyReloadFailed
	case eventType == "pii.policy_drift_detected":
		resourceType = ResourcePIIPolicyDriftDetected
	case strings.HasPrefix(eventType, "pii."):
		resourceType = ResourcePIIFindingSummary
	case strings.HasPrefix(eventType, "monitoring."):
		resourceType = ResourceMonitoringHealth
		changeType = ChangeHealthChanged
	case strings.HasPrefix(eventType, "component."):
		resourceType = ResourceComponent
		changeType = ChangeHealthChanged
	default:
		resourceType = ResourceUnknown
	}
	return resourceType, resourceID, changeType
}

// Freshness is RFC-010 §10's per-event freshness: how current the fact was when it left the server.
type Freshness struct {
	// Status is FRESH, LAGGING (published long after it happened) or REPLAYED (sent from the durable log after a reconnect, so its
	// age says nothing about the pipeline's health).
	Status          string `json:"status"`
	ProjectionLagMs int64  `json:"projection_lag_ms"`
}

// Envelope is what goes over the wire for every live event.
type Envelope struct {
	// the four keys every existing client already reads
	ID      uint      `json:"id"`
	Type    string    `json:"type"`
	Subject string    `json:"subject"`
	At      time.Time `json:"at"`

	// RFC-010 §10
	LiveEventID   string    `json:"live_event_id"`
	SchemaVersion int       `json:"schema_version"`
	PublishedAt   time.Time `json:"published_at"`
	ObservedAt    time.Time `json:"observed_at"`
	ResourceType  string    `json:"resource_type"`
	ResourceID    string    `json:"resource_id"`
	ChangeType    string    `json:"change_type"`
	// ChangeSeq is the event's id: monotonic across the whole system, so a client that has seen change_seq N for a resource can
	// reject any later-arriving update with a smaller one (RFC-010 §10 "stale-update rejection", §13 ordering).
	ChangeSeq   uint        `json:"change_seq"`
	PayloadMode string      `json:"payload_mode"`
	Payload     interface{} `json:"payload"`
	Freshness   Freshness   `json:"freshness"`

	ExecutionChainID string `json:"execution_chain_id,omitempty"`
	AttemptID        string `json:"attempt_id,omitempty"`
}

// NewEnvelope takes an event, the moment it is being published and whether it comes from the replay path, and returns its wire form.
func NewEnvelope(e Event, now time.Time, replayed bool) Envelope {
	resourceType, resourceID, changeType := Describe(e.Type, e.Subject)

	observed := e.At
	if observed.IsZero() {
		observed = now
	}
	lag := now.Sub(observed).Milliseconds()
	if lag < 0 {
		lag = 0 // clock skew must not produce a negative lag
	}

	status := "FRESH"
	switch {
	case replayed:
		status = "REPLAYED"
	case lag >= freshLagMs:
		status = "LAGGING"
	}

	liveID := ""
	if e.ID != 0 {
		liveID = strconv.FormatUint(uint64(e.ID), 10)
	}

	return Envelope{
		ID: e.ID, Type: e.Type, Subject: e.Subject, At: e.At,
		LiveEventID: liveID, SchemaVersion: SchemaVersion, PublishedAt: now.UTC(), ObservedAt: observed.UTC(),
		ResourceType: resourceType, ResourceID: resourceID, ChangeType: changeType,
		ChangeSeq: e.ID, PayloadMode: PayloadModeNone, Payload: nil,
		Freshness:        Freshness{Status: status, ProjectionLagMs: lag},
		ExecutionChainID: e.ChainID, AttemptID: e.AttemptID,
	}
}

// ResyncFrame is the body of the "resync" frame (RFC-010 §11 RESYNC_REQUIRED). Existing clients ignore the body.
type ResyncFrame struct {
	ChangeType string `json:"change_type"`
	Reason     string `json:"reason"`
}

// NewResyncFrame is sent when a replay gap is too large to fill (see the 500-event cap in the live handler).
func NewResyncFrame() ResyncFrame {
	return ResyncFrame{ChangeType: ChangeResyncRequired, Reason: "REPLAY_GAP"}
}

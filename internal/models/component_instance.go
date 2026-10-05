package models

import "time"

// RFC-010 §8 Whole-Project Component Monitoring.
//
// Before this, the dashboard knew about component TYPES ("Worker is healthy")
// but had no way to say "worker instance A is healthy, instance B is offline".
// This is the shared shape every component type is reported in, so the
// Components view and the live transition detector both read one structure
// instead of each inventing its own per-type query.
type ComponentInstance struct {
	// ComponentType is the kind of process ("Worker", "Scheduler", ...).
	ComponentType string `json:"component_type"`
	// InstanceID identifies ONE running process of that type. For workers this
	// is the worker's own instance id, which already exists and is already
	// unique per process start.
	InstanceID string `json:"instance_id"`
	// DisplayName is what an operator recognises it by (the worker id, etc).
	DisplayName string `json:"display_name"`
	// StartedAt is when this specific process started.
	StartedAt time.Time `json:"started_at"`
	// LastSeenAt is its most recent liveness signal. A zero value means it has
	// never reported one -- which is NOT the same as "very old" (see
	// InstanceVerdict).
	LastSeenAt time.Time `json:"last_seen_at"`
}

// InstanceVerdict takes one instance's last-seen time plus the two age
// thresholds, and returns a health verdict for that single instance by
// comparing how long ago it was last seen against them.
//
// A never-seen instance returns UNKNOWN, not OFFLINE: "we have no evidence"
// and "we have evidence it is gone" are different facts, and reporting the
// first as the second is exactly the zero-timestamp bug fixed earlier in this
// project.
func InstanceVerdict(inst ComponentInstance, now time.Time, degradedAfter, offlineAfter time.Duration) string {
	if inst.LastSeenAt.IsZero() || inst.LastSeenAt.Year() <= 1 {
		return "UNKNOWN"
	}

	age := now.Sub(inst.LastSeenAt)
	switch {
	case age > offlineAfter:
		return "OFFLINE"
	case age > degradedAfter:
		return "DEGRADED"
	default:
		return "HEALTHY"
	}
}

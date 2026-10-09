package models

import (
	"fmt"
	"time"
)

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
	// BuildRevision is the build this process was started from (RFC-005 §7).
	BuildRevision string `json:"build_revision"`
	// StoppedAt is when the process shut down on purpose (RFC-010 §9). Zero for a running or crashed process.
	StoppedAt time.Time `json:"stopped_at"`
	// DrainingAt is when the process saw the shutdown signal and stopped claiming new work (RFC-010 §9). Zero when it never did.
	DrainingAt time.Time `json:"draining_at"`
}

const (
	// StartupGrace is how long a freshly registered instance may go without its first heartbeat before that is worth remarking on.
	StartupGrace = 30 * time.Second
	// stopTolerance: a heartbeat already in flight when shutdown was signalled can land a moment after stopped_at is written.
	stopTolerance = 2 * time.Second
)

// GracefullyStopped reports whether the instance shut down on purpose and has not reported since (RFC-010 §9: "a missing heartbeat
// must account for startup, shutdown, drain and expected maintenance when those signals are available").
func (inst ComponentInstance) GracefullyStopped() bool {
	if inst.StoppedAt.IsZero() || inst.StoppedAt.Year() <= 1 {
		return false
	}
	return !inst.StoppedAt.Before(inst.LastSeenAt.Add(-stopTolerance))
}

// DrainWindow is how long a "draining" claim is believed. A process drains by finishing the task it already holds, which is bounded
// by the shutdown timeout; if it is still silent after the window it is treated by the ordinary heartbeat rules again, so a process
// that died while draining cannot look healthy forever.
const DrainWindow = 5 * time.Minute

// Draining reports whether the instance is shutting down but has not stopped yet: it said so recently (within DrainWindow), and has
// not recorded a stop. Draining is a normal, expected state (RFC-010 §9), not a fault.
func (inst ComponentInstance) Draining(now time.Time) bool {
	if inst.DrainingAt.IsZero() || inst.DrainingAt.Year() <= 1 || inst.GracefullyStopped() {
		return false
	}
	return now.Sub(inst.DrainingAt) < DrainWindow
}

// Starting reports whether the instance registered so recently that having no heartbeat yet is expected.
func (inst ComponentInstance) Starting(now time.Time) bool {
	return (inst.LastSeenAt.IsZero() || inst.LastSeenAt.Year() <= 1) && !inst.StartedAt.IsZero() && now.Sub(inst.StartedAt) < StartupGrace
}

// The heartbeat age thresholds, in ONE place. The monitoring sweep, the worker list and the component report all use them,
// so a worker can never be "healthy" in one view and "offline" in another.
const (
	HeartbeatDegradedAfter = 60 * time.Second
	HeartbeatOfflineAfter  = 300 * time.Second
)

// InstanceHealth takes an instance, the time and the two thresholds, and returns its verdict (as InstanceVerdict) and a
// human-readable reason with the numbers behind it.
func InstanceHealth(inst ComponentInstance, now time.Time, degradedAfter, offlineAfter time.Duration) (string, string) {
	verdict := InstanceVerdict(inst, now, degradedAfter, offlineAfter)
	if inst.GracefullyStopped() {
		return verdict, fmt.Sprintf("stopped gracefully at %s", inst.StoppedAt.UTC().Format(time.RFC3339))
	}
	if inst.Draining(now) {
		return verdict, fmt.Sprintf("draining since %s: finishing the work it holds, not claiming new tasks", inst.DrainingAt.UTC().Format(time.RFC3339))
	}
	if verdict == "UNKNOWN" {
		if inst.Starting(now) {
			return verdict, fmt.Sprintf("starting up (registered %s ago, no heartbeat yet)", now.Sub(inst.StartedAt).Round(time.Second))
		}
		return verdict, "no heartbeat has ever been recorded"
	}
	age := now.Sub(inst.LastSeenAt).Round(time.Second)
	return verdict, fmt.Sprintf("last heartbeat %s ago (degraded after %s, offline after %s)", age, degradedAfter, offlineAfter)
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
	// A deliberate shutdown is known, not inferred: no need to wait out the heartbeat thresholds to call it OFFLINE.
	if inst.GracefullyStopped() {
		return "OFFLINE"
	}
	// Draining is expected: the process has stopped heartbeating on purpose while it finishes what it holds. It is healthy, with the
	// reason saying so.
	if inst.Draining(now) {
		return "HEALTHY"
	}
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

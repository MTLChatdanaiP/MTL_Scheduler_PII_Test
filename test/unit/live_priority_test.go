package unit_test

import (
	"testing"
	"time"

	"MTL_Scheduler_PII_Test/internal/live"
	"MTL_Scheduler_PII_Test/internal/models"
)

// RFC-010 §20 Event Priority

func TestPriorityOf_CriticalTier(t *testing.T) {
	// every event type the RFC names in the "critical operational change" tier
	// that this system actually emits
	for _, et := range []string{
		"task.failed", "run.lost", "worker.offline", "queue.no_consumer",
		"alert.opened", "schedule.missed", "monitoring.degraded", "component.offline",
	} {
		if got := live.PriorityOf(et); got != live.PriorityCritical {
			t.Errorf("PriorityOf(%q) = %v, want Critical", et, got)
		}
	}
}

func TestPriorityOf_InformationalTier(t *testing.T) {
	for _, et := range []string{
		"task.progress", "attempt.heartbeat", "queue.pending_count",
		"worker.heartbeat_age", "worker.capacity_used",
	} {
		if got := live.PriorityOf(et); got != live.PriorityInformational {
			t.Errorf("PriorityOf(%q) = %v, want Informational", et, got)
		}
	}
}

func TestPriorityOf_UnknownDefaultsToNormal_TheSafeDirection(t *testing.T) {
	// An unrecognised event must never be treated as droppable. Normal is the
	// safe default because Normal is never silently dropped.
	for _, et := range []string{"task.created", "some.future.event", ""} {
		if got := live.PriorityOf(et); got != live.PriorityNormal {
			t.Errorf("PriorityOf(%q) = %v, want Normal", et, got)
		}
	}
}

func TestPriorityOf_TaskStartedIsNotCritical_OnlyFailureIs(t *testing.T) {
	if live.PriorityOf("task.started") == live.PriorityCritical {
		t.Error("task.started must not be critical -- only terminal/failure states are")
	}
}

// RFC-010 §8/§9 per-instance health verdicts

func TestInstanceVerdict(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	degraded, offline := 30*time.Second, 2*time.Minute

	mk := func(lastSeen time.Time) models.ComponentInstance {
		return models.ComponentInstance{ComponentType: "Worker", InstanceID: "i-1", LastSeenAt: lastSeen}
	}

	cases := []struct {
		name     string
		lastSeen time.Time
		want     string
	}{
		{"just seen", now.Add(-time.Second), "HEALTHY"},
		{"exactly on the degraded threshold is still healthy", now.Add(-degraded), "HEALTHY"},
		{"past degraded", now.Add(-time.Minute), "DEGRADED"},
		{"exactly on the offline threshold is still only degraded", now.Add(-offline), "DEGRADED"},
		{"past offline", now.Add(-5 * time.Minute), "OFFLINE"},
		{"never seen is UNKNOWN, not OFFLINE", time.Time{}, "UNKNOWN"},
	}

	for _, c := range cases {
		if got := models.InstanceVerdict(mk(c.lastSeen), now, degraded, offline); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestInstanceVerdict_GoZeroValueTimestampIsNotAncient(t *testing.T) {
	// The exact bug class fixed earlier in this project: Go's zero time is
	// year 1, which naive age math reports as ~2000 years stale.
	now := time.Now().UTC()
	inst := models.ComponentInstance{InstanceID: "never-reported"}
	if got := models.InstanceVerdict(inst, now, 30*time.Second, 2*time.Minute); got != "UNKNOWN" {
		t.Errorf("a zero timestamp must read UNKNOWN (no evidence), got %s", got)
	}
}

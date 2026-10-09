package models

import (
	"strings"
	"testing"
	"time"
)

// Batch 7 (RFC-010 §9): draining is a state of its own: healthy, explained, and bounded in time.
func TestDraining_ReadsHealthyWithAReasonNotDegradedOrOffline(t *testing.T) {
	now := time.Now().UTC()
	// its last heartbeat is 10 minutes old (the heartbeat stops when shutdown begins), but it said "draining" 20 seconds ago
	inst := ComponentInstance{LastSeenAt: now.Add(-10 * time.Minute), DrainingAt: now.Add(-20 * time.Second)}

	verdict, reason := InstanceHealth(inst, now, HeartbeatDegradedAfter, HeartbeatOfflineAfter)
	if verdict != "HEALTHY" || !strings.Contains(reason, "draining") {
		t.Fatalf("got %s / %q, want HEALTHY with a draining reason", verdict, reason)
	}
}

func TestDraining_IsBoundedSoAProcessThatDiedWhileDrainingIsCaught(t *testing.T) {
	now := time.Now().UTC()
	inst := ComponentInstance{LastSeenAt: now.Add(-30 * time.Minute), DrainingAt: now.Add(-DrainWindow - time.Second)}

	if inst.Draining(now) {
		t.Fatal("a draining claim older than DrainWindow must not be believed")
	}
	if verdict, _ := InstanceHealth(inst, now, HeartbeatDegradedAfter, HeartbeatOfflineAfter); verdict != "OFFLINE" {
		t.Fatalf("a process silent for 30 minutes that stopped draining long ago must read OFFLINE, got %s", verdict)
	}
}

func TestDraining_AStopEndsIt(t *testing.T) {
	now := time.Now().UTC()
	inst := ComponentInstance{LastSeenAt: now.Add(-time.Minute), DrainingAt: now.Add(-30 * time.Second), StoppedAt: now}

	if inst.Draining(now) {
		t.Fatal("once the stop is recorded the instance is stopped, not draining")
	}
	verdict, reason := InstanceHealth(inst, now, HeartbeatDegradedAfter, HeartbeatOfflineAfter)
	if verdict != "OFFLINE" || !strings.Contains(reason, "stopped gracefully") {
		t.Fatalf("got %s / %q", verdict, reason)
	}
}

func TestDraining_NeverDrainedMeansNotDraining(t *testing.T) {
	if (ComponentInstance{LastSeenAt: time.Now()}).Draining(time.Now()) {
		t.Fatal("a zero DrainingAt is not draining")
	}
}

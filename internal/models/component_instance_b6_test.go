package models

import (
	"strings"
	"testing"
	"time"
)

// RFC-010 §9: startup and shutdown are accounted for before a missing heartbeat is called a fault.

func TestInstanceHealth_GracefulStopIsOfflineImmediatelyWithAnExplicitReason(t *testing.T) {
	now := time.Now().UTC()
	inst := ComponentInstance{
		InstanceID: "i1", StartedAt: now.Add(-time.Hour),
		LastSeenAt: now.Add(-5 * time.Second), // would be HEALTHY by age alone
		StoppedAt:  now.Add(-4 * time.Second),
	}
	verdict, reason := InstanceHealth(inst, now, HeartbeatDegradedAfter, HeartbeatOfflineAfter)
	if verdict != "OFFLINE" {
		t.Fatalf("a deliberate stop is known, so OFFLINE straight away, got %s", verdict)
	}
	if !strings.Contains(reason, "stopped gracefully") {
		t.Fatalf("the reason must say it was deliberate, got %q", reason)
	}
}

func TestInstanceHealth_ACrashHasNoStoppedAtAndKeepsTheAgeRules(t *testing.T) {
	now := time.Now().UTC()
	crashed := ComponentInstance{InstanceID: "i1", StartedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-10 * time.Minute)}
	verdict, reason := InstanceHealth(crashed, now, HeartbeatDegradedAfter, HeartbeatOfflineAfter)
	if verdict != "OFFLINE" || strings.Contains(reason, "gracefully") {
		t.Fatalf("a crashed instance is OFFLINE by age and is NOT described as graceful: %s / %q", verdict, reason)
	}
	if crashed.GracefullyStopped() {
		t.Fatal("no stopped_at means not graceful")
	}

	recent := ComponentInstance{InstanceID: "i2", StartedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-5 * time.Second)}
	if v, _ := InstanceHealth(recent, now, HeartbeatDegradedAfter, HeartbeatOfflineAfter); v != "HEALTHY" {
		t.Fatalf("a recently seen, never-stopped instance is HEALTHY, got %s", v)
	}
}

func TestGracefullyStopped_AHeartbeatLandingJustAfterStopStillCountsButALaterOneDoesNot(t *testing.T) {
	now := time.Now().UTC()
	stop := now.Add(-time.Minute)

	inFlight := ComponentInstance{LastSeenAt: stop.Add(time.Second), StoppedAt: stop}
	if !inFlight.GracefullyStopped() {
		t.Error("a heartbeat already in flight when shutdown was signalled can land 1 s after stopped_at; that is still a graceful stop")
	}

	alive := ComponentInstance{LastSeenAt: stop.Add(30 * time.Second), StoppedAt: stop}
	if alive.GracefullyStopped() {
		t.Error("an instance that kept reporting well after stopped_at is not stopped")
	}
}

func TestInstanceHealth_StartupIsExplainedNotCalledAFault(t *testing.T) {
	now := time.Now().UTC()
	starting := ComponentInstance{InstanceID: "i1", StartedAt: now.Add(-8 * time.Second)} // registered, no heartbeat yet
	verdict, reason := InstanceHealth(starting, now, HeartbeatDegradedAfter, HeartbeatOfflineAfter)
	if verdict != "UNKNOWN" || !strings.Contains(reason, "starting up") {
		t.Fatalf("a just-registered instance is UNKNOWN with a startup reason, got %s / %q", verdict, reason)
	}

	stale := ComponentInstance{InstanceID: "i2", StartedAt: now.Add(-10 * time.Minute)} // registered long ago, never heartbeated
	verdict, reason = InstanceHealth(stale, now, HeartbeatDegradedAfter, HeartbeatOfflineAfter)
	if verdict != "UNKNOWN" || strings.Contains(reason, "starting up") {
		t.Fatalf("after the grace period 'never heartbeated' is no longer 'starting up': %s / %q", verdict, reason)
	}
}

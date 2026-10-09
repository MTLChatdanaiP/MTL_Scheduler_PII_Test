package models

import (
	"strings"
	"testing"
	"time"
)

func TestInstanceHealth_Boundaries(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	seen := func(ago time.Duration) ComponentInstance { return ComponentInstance{LastSeenAt: now.Add(-ago)} }

	tests := []struct {
		name string
		inst ComponentInstance
		want string
	}{
		{"just heard from", seen(2 * time.Second), "HEALTHY"},
		{"exactly at the degraded threshold is still healthy", seen(HeartbeatDegradedAfter), "HEALTHY"},
		{"just over it", seen(HeartbeatDegradedAfter + time.Second), "DEGRADED"},
		{"exactly at the offline threshold is still degraded", seen(HeartbeatOfflineAfter), "DEGRADED"},
		{"just over it", seen(HeartbeatOfflineAfter + time.Second), "OFFLINE"},
		{"never heard from is UNKNOWN, not OFFLINE", ComponentInstance{}, "UNKNOWN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict, reason := InstanceHealth(tt.inst, now, HeartbeatDegradedAfter, HeartbeatOfflineAfter)
			if verdict != tt.want {
				t.Fatalf("verdict = %s, want %s", verdict, tt.want)
			}
			if reason == "" {
				t.Fatal("a verdict always comes with its reason")
			}
		})
	}
}

func TestInstanceHealth_TheReasonShowsTheNumbersBehindIt(t *testing.T) {
	now := time.Now().UTC()
	_, reason := InstanceHealth(ComponentInstance{LastSeenAt: now.Add(-90 * time.Second)}, now, HeartbeatDegradedAfter, HeartbeatOfflineAfter)

	for _, want := range []string{"1m30s", "1m0s", "5m0s"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q should mention %q", reason, want)
		}
	}
	if _, r := InstanceHealth(ComponentInstance{}, now, HeartbeatDegradedAfter, HeartbeatOfflineAfter); !strings.Contains(r, "ever been recorded") {
		t.Errorf("a never-seen instance must say so: %q", r)
	}
}

func TestHeartbeatThresholds_AreTheValuesTheMonitoringSweepAlwaysUsed(t *testing.T) {
	if HeartbeatDegradedAfter != 60*time.Second || HeartbeatOfflineAfter != 300*time.Second {
		t.Fatalf("moving these changes when every view reports a worker as degraded or offline: %v %v", HeartbeatDegradedAfter, HeartbeatOfflineAfter)
	}
}

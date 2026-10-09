package unit_test

import (
	"MTL_Scheduler_PII_Test/internal/models"
	"strings"
	"testing"
	"time"
)

func TestInstanceHealth_Boundaries(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	seen := func(ago time.Duration) models.ComponentInstance {
		return models.ComponentInstance{LastSeenAt: now.Add(-ago)}
	}

	tests := []struct {
		name string
		inst models.ComponentInstance
		want string
	}{
		{"just heard from", seen(2 * time.Second), "HEALTHY"},
		{"exactly at the degraded threshold is still healthy", seen(models.HeartbeatDegradedAfter), "HEALTHY"},
		{"just over it", seen(models.HeartbeatDegradedAfter + time.Second), "DEGRADED"},
		{"exactly at the offline threshold is still degraded", seen(models.HeartbeatOfflineAfter), "DEGRADED"},
		{"just over it", seen(models.HeartbeatOfflineAfter + time.Second), "OFFLINE"},
		{"never heard from is UNKNOWN, not OFFLINE", models.ComponentInstance{}, "UNKNOWN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict, reason := models.InstanceHealth(tt.inst, now, models.HeartbeatDegradedAfter, models.HeartbeatOfflineAfter)
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
	_, reason := models.InstanceHealth(models.ComponentInstance{LastSeenAt: now.Add(-90 * time.Second)}, now, models.HeartbeatDegradedAfter, models.HeartbeatOfflineAfter)

	for _, want := range []string{"1m30s", "1m0s", "5m0s"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q should mention %q", reason, want)
		}
	}
	if _, r := models.InstanceHealth(models.ComponentInstance{}, now, models.HeartbeatDegradedAfter, models.HeartbeatOfflineAfter); !strings.Contains(r, "ever been recorded") {
		t.Errorf("a never-seen instance must say so: %q", r)
	}
}

func TestHeartbeatThresholds_AreTheValuesTheMonitoringSweepAlwaysUsed(t *testing.T) {
	if models.HeartbeatDegradedAfter != 60*time.Second || models.HeartbeatOfflineAfter != 300*time.Second {
		t.Fatalf("moving these changes when every view reports a worker as degraded or offline: %v %v", models.HeartbeatDegradedAfter, models.HeartbeatOfflineAfter)
	}
}

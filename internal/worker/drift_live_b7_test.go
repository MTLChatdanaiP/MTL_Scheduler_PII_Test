package worker

// Batch 7, RFC-010 §7 PII_POLICY_DRIFT_DETECTED: the whole chain. A policy file edited on disk -> the sweep writes the event -> the live
// layer names it as the drift resource class -> a client subscribed to pii.summary gets it, live and on replay.

import (
	"context"
	"testing"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/live"
	"MTL_Scheduler_PII_Test/internal/models"
)

func TestPolicyDrift_ReachesAPIISummarySubscriberLiveAndOnReplay(t *testing.T) {
	ctx := context.Background()
	active := artifactPolicy()
	active.Metadata.Checksum = checksum(active)
	useTestPolicy(t, active)

	driftMu.Lock()
	lastReportedDrift = ""
	driftMu.Unlock()

	var lastBefore models.EventEnvelope
	database.DB.Order("id DESC").Limit(1).Find(&lastBefore)

	edited := artifactPolicy()
	edited.Spec.Rules = append(edited.Spec.Rules, models.PolicyRule{ID: "b7-extra", Priority: 997, Action: models.PolicyAction{Type: "REDACT"}})
	checkPolicyDriftAt(ctx, policyFile(t, edited))

	var row models.EventEnvelope
	if err := database.DB.Where("event_type = ? AND id > ?", "pii.policy_drift_detected", lastBefore.ID).Order("id DESC").First(&row).Error; err != nil {
		t.Fatalf("the drift check wrote no event: %v", err)
	}

	resource, _, change := live.Describe(row.EventType, row.JobId)
	if resource != live.ResourcePIIPolicyDriftDetected || change != live.ChangeInvalidate {
		t.Fatalf("drift must be described as %s/%s, got %s/%s", live.ResourcePIIPolicyDriftDetected, live.ChangeInvalidate, resource, change)
	}

	piiScope, err := live.ParseScope("pii.summary")
	if err != nil {
		t.Fatal(err)
	}
	if !piiScope.Matches(live.Event{ID: row.ID, Type: row.EventType, Subject: row.JobId}) {
		t.Fatal("a pii.summary subscriber must receive the drift event live")
	}
	workersScope, _ := live.ParseScope("workers")
	if workersScope.Matches(live.Event{ID: row.ID, Type: row.EventType, Subject: row.JobId}) {
		t.Fatal("a workers subscriber must not receive the drift event")
	}

	// replay: the same scope selects the stored row
	cond, args, ok := piiScope.SQL()
	if !ok {
		t.Fatal("pii.summary must have a replay condition")
	}
	var replayed int64
	database.DB.Model(&models.EventEnvelope{}).Where("id = ?", row.ID).Where(cond, args...).Count(&replayed)
	if replayed != 1 {
		t.Fatal("a reconnecting pii.summary client must be able to replay the drift event")
	}
}

package worker

// RFC-006 §32 pii.policy_drift_detected from the monitoring sweep: ONE event per distinct on-disk checksum.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func driftEvents(t *testing.T) int64 {
	t.Helper()
	var n int64
	database.DB.Model(&models.EventEnvelope{}).Where("event_type = ? AND job_id = ?", "pii.policy_drift_detected", "system").Count(&n)
	return n
}

func policyFile(t *testing.T, p models.PIIPolicy) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "default.json")
	data, _ := json.Marshal(p)
	os.WriteFile(path, data, 0o644)
	return path
}

func checksum(p models.PIIPolicy) string {
	spec, _ := json.Marshal(p.Spec)
	sum := sha256.Sum256(spec)
	return hex.EncodeToString(sum[:])
}

func TestCheckPolicyDrift_OneEventPerDistinctDriftThenAgainAfterItClears(t *testing.T) {
	ctx := context.Background()
	active := artifactPolicy()
	active.Metadata.Checksum = checksum(active)
	useTestPolicy(t, active)

	driftMu.Lock()
	lastReportedDrift = ""
	driftMu.Unlock()
	before := driftEvents(t)

	// the file still matches: nothing
	same := policyFile(t, active)
	checkPolicyDriftAt(ctx, same)
	if driftEvents(t) != before {
		t.Fatal("a file that matches the active policy must write no event")
	}

	// the file is edited: ONE event however many sweeps run
	edited := artifactPolicy()
	edited.Spec.Rules = append(edited.Spec.Rules, models.PolicyRule{ID: "extra", Priority: 999, Action: models.PolicyAction{Type: "REDACT"}})
	drifted := policyFile(t, edited)
	for i := 0; i < 5; i++ {
		checkPolicyDriftAt(ctx, drifted)
	}
	if got := driftEvents(t) - before; got != 1 {
		t.Fatalf("five sweeps over the same drifted file must write ONE event, wrote %d", got)
	}

	// edited AGAIN to something different: a new distinct checksum is a new event
	edited2 := artifactPolicy()
	edited2.Spec.Rules = append(edited2.Spec.Rules, models.PolicyRule{ID: "other", Priority: 998, Action: models.PolicyAction{Type: "MASK"}})
	checkPolicyDriftAt(ctx, policyFile(t, edited2))
	if got := driftEvents(t) - before; got != 2 {
		t.Fatalf("a different on-disk checksum is a new drift, expected 2 events, got %d", got)
	}

	// reloaded (the file matches again), then edited the SAME way once more: reported again
	checkPolicyDriftAt(ctx, same)
	checkPolicyDriftAt(ctx, drifted)
	if got := driftEvents(t) - before; got != 3 {
		t.Fatalf("once the drift clears, the same edit must be reported again, expected 3 events, got %d", got)
	}
}

func TestCheckPolicyDrift_AnUnreadableFileWritesNoEvent(t *testing.T) {
	active := artifactPolicy()
	active.Metadata.Checksum = checksum(active)
	useTestPolicy(t, active)
	driftMu.Lock()
	lastReportedDrift = ""
	driftMu.Unlock()
	before := driftEvents(t)

	checkPolicyDriftAt(context.Background(), filepath.Join(t.TempDir(), "missing.json"))

	if driftEvents(t) != before {
		t.Fatal("a file that cannot be read cannot be said to have drifted")
	}
}

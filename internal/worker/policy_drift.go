package worker

import (
	"context"
	"sync"

	"MTL_Scheduler_PII_Test/internal/events"
	"MTL_Scheduler_PII_Test/internal/pii"
)

// RFC-006 §32 pii.policy_drift_detected. The monitoring sweep calls checkPolicyDrift once per pass.

var (
	driftMu           sync.Mutex
	lastReportedDrift string // the on-disk checksum already reported
)

// checkPolicyDrift compares the active policy with the file on disk and records ONE pii.policy_drift_detected event per
// distinct on-disk checksum, so a file that stays edited does not write an event every pass. Once the file matches the
// active policy again the memory is cleared, so a later edit is reported again.
func checkPolicyDrift(ctx context.Context) {
	checkPolicyDriftAt(ctx, pii.DefaultPolicyPath)
}

func checkPolicyDriftAt(ctx context.Context, path string) {
	status := pii.CheckPolicyDrift(path)

	driftMu.Lock()
	defer driftMu.Unlock()

	if !status.Drifted {
		lastReportedDrift = ""
		return
	}
	if status.FileChecksum == lastReportedDrift {
		return
	}
	lastReportedDrift = status.FileChecksum

	events.LogEvent(ctx, "system", "pii.policy_drift_detected", "monitoring")
}

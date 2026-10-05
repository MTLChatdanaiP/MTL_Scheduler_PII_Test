package unit_test

import (
	"testing"

	"MTL_Scheduler_PII_Test/internal/live"
)

// RFC-006 §32 / RFC-010 §25: each new pii.* event type must land in the right scope
// bucket, otherwise it would stream to callers who are not allowed to read it.
func TestRequiredScope_NewPIIEventTypes(t *testing.T) {
	cases := map[string]string{
		"pii.scan_failed":              "pii.findings.read",
		"pii.policy_validated":         "pii.policy.read",
		"pii.policy_validation_failed": "pii.policy.read",
	}

	for eventType, want := range cases {
		if got := live.RequiredScope(eventType); got != want {
			t.Errorf("RequiredScope(%q) = %q, want %q", eventType, got, want)
		}
	}
}

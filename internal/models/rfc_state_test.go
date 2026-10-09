package models

import "testing"

// Batch 7, RFC-001 §5: the mapping from the stored status to the RFC's state names.
func TestRFCState_Table(t *testing.T) {
	cases := []struct {
		status, category string
		retryIndex       int
		want             string
	}{
		{"Pending", "", 0, RFCStateScheduled},
		{"", "", 0, RFCStateScheduled},
		{"Queued", "", 0, RFCStateQueued},
		{"Running", "", 0, RFCStateRunning},
		{"Completed", "", 0, RFCStateSucceeded},
		{"Failed", "APPLICATION_ERROR", 0, RFCStateFailed},
		{"Failed", "", 1, RFCStateFailed},
		{"Failed", "TIMEOUT", 0, RFCStateTimedOut},
		{"Failed", "timeout", 2, RFCStateTimedOut},
		{"Failed", "UNKNOWN", MaxRetries - 1, RFCStateFailed},
		{"Failed", "APPLICATION_ERROR", MaxRetries, RFCStateDead},
		{"Failed", "TIMEOUT", MaxRetries, RFCStateDead}, // nothing more will run, which matters more than why
		{"Blocked", "", 0, RFCStateFailed},
		{"SomethingNew", "", 0, RFCStateUnknown},
	}
	for _, c := range cases {
		if got := RFCState(c.status, c.category, c.retryIndex); got != c.want {
			t.Errorf("RFCState(%q, %q, %d) = %q, want %q", c.status, c.category, c.retryIndex, got, c.want)
		}
	}
}

func TestRFCState_NeverProducesCancelled(t *testing.T) {
	// Nothing in the system cancels a run, so CANCELLED must not appear; the naming map documents it as not applicable.
	for _, status := range []string{"Pending", "Queued", "Running", "Completed", "Failed", "Blocked", "x"} {
		if RFCState(status, "", 0) == "CANCELLED" {
			t.Fatalf("status %q mapped to CANCELLED", status)
		}
	}
}

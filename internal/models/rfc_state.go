package models

// RFC-001 §5 Run State Model, as a mapping rather than a rename.
//
// The stored Task.Status values (Pending, Queued, Running, Completed, Failed) are kept: they are in the database, in every filter, in
// the dashboard and in the history. RFCState gives each run the name RFC-001 §5 uses for the same situation, so an API consumer that
// speaks the RFC can read it without knowing the stored words. RFC-000_naming_map.md documents the table.
//
// CANCELLED is not produced: nothing in this system cancels a run, so there is no stored fact to map it from.
const (
	RFCStateScheduled = "SCHEDULED" // Pending: created, waiting for its run time
	RFCStateQueued    = "QUEUED"
	RFCStateRunning   = "RUNNING"
	RFCStateSucceeded = "SUCCEEDED"
	RFCStateFailed    = "FAILED"
	RFCStateTimedOut  = "TIMED_OUT" // Failed with the TIMEOUT category
	RFCStateDead      = "DEAD"      // Failed and the retry budget is spent: nothing more will run
	RFCStateUnknown   = "UNKNOWN"   // a stored status this mapping has never heard of
)

// MaxRetries is how many retry children a chain may have (RFC-001 §7). The worker and RFCState both read it, so "retries exhausted"
// means one thing.
const MaxRetries = 3

// RFCState maps a stored run status to RFC-001 §5's name. failureCategory is the latest attempt's category (empty when there is
// none); retryIndex is the run's own RetryIndex.
func RFCState(status, failureCategory string, retryIndex int) string {
	switch status {
	case "", "Pending":
		return RFCStateScheduled
	case "Queued":
		return RFCStateQueued
	case "Running":
		return RFCStateRunning
	case "Completed":
		return RFCStateSucceeded
	case "Failed":
		if retryIndex >= MaxRetries {
			return RFCStateDead
		}
		if failureCategory != "" && NormalizeFailureCategory(failureCategory) == FailureTimeout {
			return RFCStateTimedOut
		}
		return RFCStateFailed
	case "Blocked":
		return RFCStateFailed // a blocked run never ran and never will: terminal, failed by policy
	}
	return RFCStateUnknown
}

package models

import "strings"

// RFC-001 §14 Failure Categories. The RFC calls these "suggested" and says they are
// "intentionally broader than concrete exception types". This file is the single place
// that defines them, so a typo in one handler can never create a category the rest of
// the system (the run list's failure_category filter, the timeline, monitoring) has
// never heard of.
const (
	FailureApplicationError    = "APPLICATION_ERROR"
	FailureInvalidInput        = "INVALID_INPUT"
	FailureDependencyError     = "DEPENDENCY_ERROR"
	FailureTimeout             = "TIMEOUT"
	FailureWorkerFailure       = "WORKER_FAILURE"
	FailureInfrastructureError = "INFRASTRUCTURE_ERROR"
	FailureUnknown             = "UNKNOWN"
)

// AllFailureCategories returns the categories in the order the RFC lists them
// (a copy, so callers cannot change the list).
func AllFailureCategories() []string {
	return []string{
		FailureApplicationError,
		FailureInvalidInput,
		FailureDependencyError,
		FailureTimeout,
		FailureWorkerFailure,
		FailureInfrastructureError,
		FailureUnknown,
	}
}

// NormalizeFailureCategory makes sure a failed attempt always carries a real category:
// an empty or unrecognised value becomes UNKNOWN instead of being stored as-is (an empty
// category used to be stored for every retryable failure). Case and surrounding spaces
// are forgiven.
func NormalizeFailureCategory(category string) string {
	c := strings.ToUpper(strings.TrimSpace(category))
	for _, known := range AllFailureCategories() {
		if c == known {
			return c
		}
	}
	return FailureUnknown
}

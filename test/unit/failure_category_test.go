package unit_test

import (
	"testing"

	"MTL_Scheduler_PII_Test/internal/models"
)

// RFC-001 §14 Failure Categories

func TestEveryRFCCategoryIsKnownAndKeptAsIs(t *testing.T) {
	want := []string{
		"APPLICATION_ERROR", "INVALID_INPUT", "DEPENDENCY_ERROR", "TIMEOUT",
		"WORKER_FAILURE", "INFRASTRUCTURE_ERROR", "UNKNOWN",
	}

	got := models.AllFailureCategories()
	if len(got) != len(want) {
		t.Fatalf("expected %d categories, got %d: %v", len(want), len(got), got)
	}
	for i, c := range want {
		if got[i] != c {
			t.Errorf("category %d = %q, want %q (RFC order)", i, got[i], c)
		}
		if models.NormalizeFailureCategory(c) != c {
			t.Errorf("NormalizeFailureCategory(%q) changed a valid category", c)
		}
	}
}

// Regression guard: these are the exact strings runHandler returns today. A typo in any
// of them would silently turn into UNKNOWN, so the test pins them.
func TestStringsUsedByTheWorkerHandlersAreValid(t *testing.T) {
	for _, c := range []string{"INVALID_INPUT", "DEPENDENCY_ERROR", "TIMEOUT", "APPLICATION_ERROR"} {
		if got := models.NormalizeFailureCategory(c); got != c {
			t.Errorf("handler category %q normalised to %q", c, got)
		}
	}
}

func TestEmptyCategoryBecomesUnknown(t *testing.T) {
	// The retryable-failure case: the handler returns no category.
	if got := models.NormalizeFailureCategory(""); got != models.FailureUnknown {
		t.Errorf("empty -> %q, want UNKNOWN", got)
	}
	if got := models.NormalizeFailureCategory("   "); got != models.FailureUnknown {
		t.Errorf("blank -> %q, want UNKNOWN", got)
	}
}

func TestUnrecognisedCategoryBecomesUnknown(t *testing.T) {
	for _, c := range []string{"banana", "TIME_OUT", "APPLICATION"} {
		if got := models.NormalizeFailureCategory(c); got != models.FailureUnknown {
			t.Errorf("%q -> %q, want UNKNOWN", c, got)
		}
	}
}

func TestCategoryCaseAndSpacesAreForgiven(t *testing.T) {
	if got := models.NormalizeFailureCategory("  timeout "); got != models.FailureTimeout {
		t.Errorf("got %q, want TIMEOUT", got)
	}
}

func TestReturnedCategoryListIsACopy(t *testing.T) {
	list := models.AllFailureCategories()
	list[0] = "CHANGED"
	if models.AllFailureCategories()[0] != "APPLICATION_ERROR" {
		t.Error("changing the returned list changed the real one")
	}
}

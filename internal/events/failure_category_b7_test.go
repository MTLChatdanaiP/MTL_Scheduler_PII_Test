package events

// Batch 7, RFC-001 §14: every attempt that ends badly records one of the RFC's categories, whichever way it ended.

import (
	"context"
	"testing"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func storedCategory(t *testing.T, attemptID string) string {
	t.Helper()
	var a models.Attempt
	if err := database.DB.First(&a, "attempt_id = ?", attemptID).Error; err != nil {
		t.Fatal(err)
	}
	return a.FailureCategory
}

func TestFailureCategory_EveryBadEndingRecordsAValidCategory(t *testing.T) {
	ctx := context.Background()
	valid := map[string]bool{}
	for _, c := range models.AllFailureCategories() {
		valid[c] = true
	}

	type ending struct {
		name string
		do   func(a *models.Attempt)
		want string // "" = any valid category
	}
	endings := []ending{
		{"failed with a real category", func(a *models.Attempt) { MarkAttemptFailed(ctx, a, "TIMEOUT") }, "TIMEOUT"},
		{"failed with no category (a retryable failure)", func(a *models.Attempt) { MarkAttemptFailed(ctx, a, "") }, "UNKNOWN"},
		{"failed with a made-up category", func(a *models.Attempt) { MarkAttemptFailed(ctx, a, "BANANA") }, "UNKNOWN"},
		{"failed with a lower-case category", func(a *models.Attempt) { MarkAttemptFailed(ctx, a, " dependency_error ") }, "DEPENDENCY_ERROR"},
		{"abandoned with a stated cause", func(a *models.Attempt) { MarkAttemptAbandonedFor(ctx, a, "INFRASTRUCTURE_ERROR") }, "INFRASTRUCTURE_ERROR"},
		{"abandoned with no cause", func(a *models.Attempt) { MarkAttemptAbandoned(ctx, a) }, "WORKER_FAILURE"},
	}
	for i, e := range endings {
		task := makeTask(t, nil)
		attempt := MarkAttemptClaimed(ctx, task.JobId, "worker-b7", i+1)
		e.do(&attempt)

		got := storedCategory(t, attempt.AttemptId)
		if !valid[got] {
			t.Errorf("%s: stored category %q is not one of the RFC-001 §14 categories", e.name, got)
		}
		if e.want != "" && got != e.want {
			t.Errorf("%s: stored %q, want %q", e.name, got, e.want)
		}
	}
}

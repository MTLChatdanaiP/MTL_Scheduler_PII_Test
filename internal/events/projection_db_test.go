package events

// RFC-005 §7: the run projection written by the real LogEvent path, against the real database.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func projectionOf(t *testing.T, jobID string) models.RunProjection {
	t.Helper()
	var p models.RunProjection
	if err := database.DB.Where("job_id = ?", jobID).First(&p).Error; err != nil {
		t.Fatalf("no projection for %s: %v", jobID, err)
	}
	return p
}

func projectionRows(t *testing.T, jobID string) int64 {
	t.Helper()
	var n int64
	database.DB.Unscoped().Model(&models.RunProjection{}).Where("job_id = ?", jobID).Count(&n)
	return n
}

func eventTime(t *testing.T, jobID, eventType string) time.Time {
	t.Helper()
	return lastEvent(t, jobID, eventType).OccurredAt
}

func TestProjection_AFailedRunKeepsItsFailedStatusThroughTheRealPath(t *testing.T) {
	ctx := context.Background()
	task := makeTask(t, nil)

	for _, et := range []string{"task.created", "task.queued", "task.started", "task.progress", "pii.detected", "task.failed", "task.retries_exhausted", "task.timed_out"} {
		LogEvent(ctx, task.JobId, et, "test")
	}

	p := projectionOf(t, task.JobId)
	if p.CurrentStatus != "Failed" {
		t.Fatalf("current_status = %q: the dashboard badge reads this, and it was '' for every failed run", p.CurrentStatus)
	}
}

func TestProjection_TheFirstEventPopulatesTheRowAndTimesAreTheEventsOwn(t *testing.T) {
	task := makeTask(t, nil)

	LogEvent(context.Background(), task.JobId, "task.started", "test")

	p := projectionOf(t, task.JobId)
	if p.CurrentStatus != "Running" {
		t.Fatalf("the first event ignored its own status: %q (the old writer inserted an empty row)", p.CurrentStatus)
	}
	want := eventTime(t, task.JobId, "task.started")
	if !sameTime(p.StartedAt, want) || !sameTime(p.LastEventAt, want) {
		t.Fatalf("started_at/last_event_at must equal the event's occurred_at %v, got %v / %v", want, p.StartedAt, p.LastEventAt)
	}
}

func TestProjection_OnlyRunsGetARow(t *testing.T) {
	ctx := context.Background()
	subjects := map[string]string{
		"system":                         "pii.policy_activated",
		"worker-" + ulid.Make().String(): "worker.offline",
		"queue-" + ulid.Make().String():  "queue.degraded",
		"sched-" + ulid.Make().String():  "schedule.created",
		"alert-" + ulid.Make().String():  "alert.opened",
		"ghost-" + ulid.Make().String():  "task.created", // a run id with no task
	}
	for subject, et := range subjects {
		forgetSubject(t, subject)
		LogEvent(ctx, subject, et, "test")
		if n := projectionRows(t, subject); n != 0 {
			t.Errorf("%s event for %q created %d projection row(s): only a run that exists is projected", et, subject, n)
		}
	}
}

func TestProjection_CountsComeFromTheirTables(t *testing.T) {
	ctx := context.Background()
	task := makeTask(t, nil)
	database.DB.Create(&models.PIIRecord{JobID: task.JobId, Type: "Email", Source: "JOB_PAYLOAD", FingerprintValue: "a"})
	database.DB.Create(&models.PIIRecord{JobID: task.JobId, Type: "Phone", Source: "JOB_PAYLOAD", FingerprintValue: "b"})
	t.Cleanup(func() { database.DB.Unscoped().Where("job_id = ?", task.JobId).Delete(&models.PIIRecord{}) })

	LogEvent(ctx, task.JobId, "task.created", "test")
	if got := projectionOf(t, task.JobId).PIIFindingCount; got != 2 {
		t.Fatalf("pii_finding_count = %d, want 2 (it existed on the model and was never written)", got)
	}

	MarkAttemptClaimed(ctx, task.JobId, "worker-A", 1)
	second := MarkAttemptClaimed(ctx, task.JobId, "worker-B", 2)
	p := projectionOf(t, task.JobId)
	if p.AttemptCount != 2 || p.LatestAttemptID != second.AttemptId || p.LatestWorkerID != "worker-B" || p.LatestAttemptStatus != "Claimed" {
		t.Fatalf("latest attempt not tracked: %+v", p)
	}

	// a repeated event cannot inflate a count, because counts are recomputed rather than incremented
	LogEvent(ctx, task.JobId, "attempt.claimed", "test")
	LogEvent(ctx, task.JobId, "attempt.claimed", "test")
	if got := projectionOf(t, task.JobId).AttemptCount; got != 2 {
		t.Fatalf("a replayed event changed attempt_count to %d", got)
	}
}

func TestProjection_TwoWritersForOneRunDoNotLoseEachOthersUpdates(t *testing.T) {
	ctx := context.Background()
	task := makeTask(t, nil)

	var wg sync.WaitGroup
	types := []string{"task.created", "task.queued", "task.started", "task.completed"}
	for i := 0; i < 24; i++ {
		et := types[i%len(types)]
		if i >= 4 {
			et = "task.progress"
		}
		wg.Add(1)
		go func(et string) {
			defer wg.Done()
			LogEvent(ctx, task.JobId, et, "test")
		}(et)
	}
	wg.Wait()

	if n := projectionRows(t, task.JobId); n != 1 {
		t.Fatalf("concurrent first events must produce ONE row, got %d", n)
	}
	p := projectionOf(t, task.JobId)
	if p.CurrentStatus != "Completed" {
		t.Fatalf("a lost update left status %q, want Completed", p.CurrentStatus)
	}
	if p.StartedAt.IsZero() || p.QueuedAt.IsZero() || p.CompletedAt.IsZero() {
		t.Fatalf("every lifecycle time must survive concurrent writers: %+v", p)
	}
}

func TestRefreshRunProjectionCounts_AnnotationsAndAlertsAreRecomputedBothWays(t *testing.T) {
	ctx := context.Background()
	task := makeTask(t, nil)
	LogEvent(ctx, task.JobId, "task.created", "test")

	annotation := models.MonitoringAnnotation{AnnotationID: ulid.Make().String(), Type: "RUN_STUCK", SubjectType: "TASK", SubjectID: task.JobId}
	alert := models.Alert{AlertID: ulid.Make().String(), AlertType: "RUN_STUCK", Severity: "CRITICAL", Status: "OPEN", SubjectType: "RUN", SubjectID: task.JobId}
	database.DB.Create(&annotation)
	database.DB.Create(&alert)
	t.Cleanup(func() {
		database.DB.Unscoped().Where("subject_id = ?", task.JobId).Delete(&models.MonitoringAnnotation{})
		database.DB.Unscoped().Where("subject_id = ?", task.JobId).Delete(&models.Alert{})
	})

	RefreshRunProjectionCounts(ctx)
	p := projectionOf(t, task.JobId)
	if p.ActiveAnnotationCount != 1 || p.OpenAlertCount != 1 {
		t.Fatalf("expected 1 annotation and 1 open alert, got %d and %d", p.ActiveAnnotationCount, p.OpenAlertCount)
	}

	now := time.Now().UTC()
	database.DB.Model(&annotation).Update("resolved_at", now)
	database.DB.Model(&alert).Update("status", "RESOLVED")

	RefreshRunProjectionCounts(ctx)
	RefreshRunProjectionCounts(ctx) // idempotent
	p = projectionOf(t, task.JobId)
	if p.ActiveAnnotationCount != 0 || p.OpenAlertCount != 0 {
		t.Fatalf("resolved items must drop out of the counts, got %d and %d", p.ActiveAnnotationCount, p.OpenAlertCount)
	}
}

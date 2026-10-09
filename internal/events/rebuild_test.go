package events

// RFC-005 §4/§7: a projection is derived, so history can be repaired by replaying events. Every rebuild here is scoped
// to job ids with a unique prefix, so it can only ever touch rows these tests created.

import (
	"context"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func prefixedTask(t *testing.T, prefix string, status string) models.Task {
	t.Helper()
	return makeTask(t, func(k *models.Task) {
		k.JobId = prefix + "-" + ulid.Make().String()
		k.ExecutionChainId = k.JobId
		k.Status = status
	})
}

func logRun(ctx context.Context, jobID string, types ...string) {
	for _, et := range types {
		LogEvent(ctx, jobID, et, "test")
	}
}

func TestRebuild_ReplayingTheEventLogReproducesWhatTheLiveWriterBuilt(t *testing.T) {
	ctx := context.Background()
	prefix := "rb-" + ulid.Make().String()
	task := prefixedTask(t, prefix, "Failed")
	logRun(ctx, task.JobId, "task.created", "task.queued", "task.started", "task.progress", "pii.detected", "task.failed")

	report, err := rebuildRunProjections(ctx, false, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if report.Wrong != 0 || report.Missing != 0 {
		t.Fatalf("a rebuild from events must equal the live projection exactly (the same reducer is used for both), got %+v", report)
	}
}

func TestRebuild_RepairsABlankStatusAndWrongCountsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	prefix := "rb-" + ulid.Make().String()
	task := prefixedTask(t, prefix, "Failed")
	logRun(ctx, task.JobId, "task.created", "task.queued", "task.started", "task.failed")

	// the state your database is in today: blank status, wrong count
	database.DB.Model(&models.RunProjection{}).Where("job_id = ?", task.JobId).UpdateColumns(map[string]interface{}{"current_status": "", "attempt_count": 99})

	changed, err := RebuildRunProjection(ctx, task.JobId)
	if err != nil || !changed {
		t.Fatalf("a corrupted projection must be repaired: changed=%v err=%v", changed, err)
	}
	p := projectionOf(t, task.JobId)
	if p.CurrentStatus != "Failed" || p.AttemptCount != 0 {
		t.Fatalf("not repaired: %+v", p)
	}

	if changed, _ := RebuildRunProjection(ctx, task.JobId); changed {
		t.Fatal("a second rebuild must change nothing")
	}
}

func TestRebuild_CheckModeReportsAndChangesNothing(t *testing.T) {
	ctx := context.Background()
	prefix := "rb-" + ulid.Make().String()
	task := prefixedTask(t, prefix, "Failed")
	logRun(ctx, task.JobId, "task.created", "task.failed")
	database.DB.Model(&models.RunProjection{}).Where("job_id = ?", task.JobId).UpdateColumn("current_status", "")

	report, _ := rebuildRunProjections(ctx, false, prefix)

	if report.Wrong != 1 {
		t.Fatalf("check mode must report the wrong row, got %+v", report)
	}
	if got := projectionOf(t, task.JobId).CurrentStatus; got != "" {
		t.Fatalf("check mode must NOT change anything, status is now %q", got)
	}
}

func TestRebuild_DeletesRowsThatBelongToNoTask(t *testing.T) {
	ctx := context.Background()
	prefix := "rb-" + ulid.Make().String()
	junk := []string{prefix + "-system", prefix + "-worker-1", prefix + "-tasks:stream"}
	for _, j := range junk {
		database.DB.Create(&models.RunProjection{JobId: j})
	}
	t.Cleanup(func() { database.DB.Unscoped().Where("job_id LIKE ?", prefix+"%").Delete(&models.RunProjection{}) })

	check, _ := rebuildRunProjections(ctx, false, prefix)
	if check.Junk != 3 {
		t.Fatalf("expected 3 junk rows reported, got %+v", check)
	}
	if n := projectionRowsLike(t, prefix); n != 3 {
		t.Fatalf("check mode must not delete, %d remain", n)
	}

	rebuildRunProjections(ctx, true, prefix)
	if n := projectionRowsLike(t, prefix); n != 0 {
		t.Fatalf("junk rows must be physically removed, %d remain", n)
	}
}

func projectionRowsLike(t *testing.T, prefix string) int64 {
	t.Helper()
	var n int64
	database.DB.Unscoped().Model(&models.RunProjection{}).Where("job_id LIKE ?", prefix+"%").Count(&n)
	return n
}

func TestRebuild_ARunWithNoEventsGetsARowFromItsTask(t *testing.T) {
	ctx := context.Background()
	prefix := "rb-" + ulid.Make().String()
	task := prefixedTask(t, prefix, "Completed")

	report, _ := rebuildRunProjections(ctx, true, prefix)

	if report.Missing != 1 {
		t.Fatalf("expected one missing projection, got %+v", report)
	}
	if got := projectionOf(t, task.JobId).CurrentStatus; got != "Completed" {
		t.Fatalf("a run with no events must take its status from the task, got %q", got)
	}
}

// the same events inserted in a scrambled ORDER must rebuild to the same projection
func TestRebuild_DoesNotDependOnTheOrderEventsWereStored(t *testing.T) {
	ctx := context.Background()
	prefix := "rb-" + ulid.Make().String()
	task := prefixedTask(t, prefix, "Completed")
	base := time.Now().UTC().Add(-time.Minute)

	// stored completed FIRST, then created, then started, then queued: the reverse of the truth
	for i, et := range []string{"task.completed", "task.created", "task.started", "task.queued"} {
		ts := map[string]time.Time{"task.created": base, "task.queued": base.Add(1 * time.Second), "task.started": base.Add(2 * time.Second), "task.completed": base.Add(3 * time.Second)}[et]
		database.DB.Create(&models.EventEnvelope{JobId: task.JobId, EventID: ulid.Make().String(), EventType: et, OccurredAt: ts, IngestedAt: time.Now().UTC(), Producer: "test", SchemaVersion: "1", ProducerID: "test:x", ProducerSequence: int64(i + 1)})
	}

	rebuildRunProjections(ctx, true, prefix)

	p := projectionOf(t, task.JobId)
	if p.CurrentStatus != "Completed" || p.Contradicted {
		t.Fatalf("event storage order must not matter: %+v", p)
	}
	if !sameTime(p.QueuedAt, base.Add(1*time.Second)) || !sameTime(p.StartedAt, base.Add(2*time.Second)) || !sameTime(p.CompletedAt, base.Add(3*time.Second)) {
		t.Fatalf("times must be the events' own: queued=%v started=%v completed=%v", p.QueuedAt, p.StartedAt, p.CompletedAt)
	}
}

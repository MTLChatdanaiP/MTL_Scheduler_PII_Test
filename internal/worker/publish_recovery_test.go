package worker

// RFC-003 §1: Redis is a substrate, not the source of truth, so a task that is Queued in the
// database but never reached Redis must not be lost.
//
// Before this, publishDueTasks marked a task Queued FIRST, swallowed a failed publish, and only
// ever picked up tasks that were still Pending. A task that came due while Redis was down was
// marked Queued, never delivered, and never picked up again.

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"MTL_Scheduler_PII_Test/internal/cache"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/events"
	"MTL_Scheduler_PII_Test/internal/models"
)

// deadRedis swaps in a client that cannot connect, and returns a function that puts the real
// one back. It is also restored automatically when the test ends.
func deadRedis(t *testing.T) (restore func()) {
	t.Helper()
	real := cache.Client
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 300 * time.Millisecond, MaxRetries: -1})
	cache.Client = dead

	done := false
	restore = func() {
		if !done {
			done = true
			cache.Client = real
			dead.Close()
		}
	}
	t.Cleanup(restore)
	return restore
}

var longAgo = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// force puts a task into a given state without going through the scheduler. UpdateColumns
// bypasses the automatic updated_at bump, so updated_at can be set exactly.
func force(t *testing.T, jobID string, cols map[string]interface{}) {
	t.Helper()
	if err := database.DB.Model(&models.Task{}).Where("job_id = ?", jobID).UpdateColumns(cols).Error; err != nil {
		t.Fatal(err)
	}
}

func reload(t *testing.T, jobID string) models.Task {
	t.Helper()
	var got models.Task
	if err := database.DB.Where("job_id = ?", jobID).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	return got
}

func strandedIDs(ctx context.Context) map[string]bool {
	ids := map[string]bool{}
	for _, task := range findStranded(ctx) {
		ids[task.JobId] = true
	}
	return ids
}

func mine(tasks []models.Task, jobID string) []models.Task {
	var out []models.Task
	for _, task := range tasks {
		if task.JobId == jobID {
			out = append(out, task)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// The normal path
// ---------------------------------------------------------------------------

func TestQueueAndPublish_PublishesTheEnvelopeAndRecordsTheConfirmedPublish(t *testing.T) {
	task := newRunnableTask(t, "fail_permanent", "n", `{}`)
	forgetEntries(t, task.JobId)
	force(t, task.JobId, map[string]interface{}{"trace_id": "trace-sched-1"})
	task = reload(t, task.JobId)

	queueAndPublish(context.Background(), []models.Task{task})

	got := reload(t, task.JobId)
	if got.Status != "Queued" {
		t.Fatalf("status = %s, want Queued", got.Status)
	}
	if got.PublishedAt == nil {
		t.Fatal("a confirmed publish must set PublishedAt")
	}

	entries := streamEntriesFor(t, task.JobId)
	if len(entries) != 1 {
		t.Fatalf("expected exactly one message in the stream, got %d", len(entries))
	}
	v := entries[0].Values
	if v["trace_id"] != "trace-sched-1" || v["correlation_id"] != task.ExecutionChainId || v["schema_version"] != "1" {
		t.Fatalf("the envelope on the wire is wrong: %v", v)
	}
	if _, err := time.Parse(time.RFC3339Nano, v["created_at"].(string)); err != nil {
		t.Fatalf("created_at is not a timestamp: %v", v["created_at"])
	}
}

// ---------------------------------------------------------------------------
// Redis down when the task comes due
// ---------------------------------------------------------------------------

func TestPublishDueTasks_WhenRedisIsDownTouchesNothing(t *testing.T) {
	task := newRunnableTask(t, "fail_permanent", "n", `{}`)
	forgetEntries(t, task.JobId)
	restore := deadRedis(t)

	publishDueTasks(context.Background())

	restore()
	got := reload(t, task.JobId)
	if got.Status != "Pending" {
		t.Fatalf("with Redis down the task must stay Pending so nothing is lost, status = %s", got.Status)
	}
	if n := eventCount(t, task.JobId, "task.queued"); n != 0 {
		t.Fatalf("no task.queued event should exist for work that could not be delivered, found %d", n)
	}
	if len(streamEntriesFor(t, task.JobId)) != 0 {
		t.Fatal("nothing should have been published")
	}
}

func TestRedisReachable_RecordsAtMostOneEventPerMinuteNotOnePerPoll(t *testing.T) {
	events.ResetRateLimitsForTest()

	before := eventCount(t, "", "redis.unavailable")
	restore := deadRedis(t)

	for i := 0; i < 3; i++ {
		if redisReachable(context.Background()) {
			t.Fatal("a dead Redis must not report as reachable")
		}
	}
	restore()

	if got := eventCount(t, "", "redis.unavailable"); got != before+1 {
		t.Fatalf("three polls during one outage should record one event, recorded %d", got-before)
	}
}

func TestRedisReachable_IsTrueWhenRedisAnswers(t *testing.T) {
	if !redisReachable(context.Background()) {
		t.Fatal("a working Redis must report as reachable")
	}
}

// ---------------------------------------------------------------------------
// The publish fails, or the process dies after marking the task Queued
// ---------------------------------------------------------------------------

func TestAFailedPublish_LeavesTheTaskRecoverableAndItIsRepublished(t *testing.T) {
	task := newRunnableTask(t, "fail_permanent", "n", `{}`)
	forgetEntries(t, task.JobId)
	restore := deadRedis(t)

	// Redis dies between the reachability check and the publish
	queueAndPublish(context.Background(), []models.Task{task})

	got := reload(t, task.JobId)
	if got.Status != "Queued" || got.PublishedAt != nil {
		t.Fatalf("expected Queued with no confirmed publish, got %s published=%v", got.Status, got.PublishedAt)
	}
	if n := eventCount(t, task.JobId, "task.publish_failed"); n != 1 {
		t.Fatalf("the failure must be recorded, found %d task.publish_failed events", n)
	}

	restore()
	force(t, task.JobId, map[string]interface{}{"updated_at": longAgo}) // the passage of time

	if !strandedIDs(context.Background())[task.JobId] {
		t.Fatal("a task that stayed Queued without a confirmed publish must be found as stranded")
	}

	publishStrandedTasks(context.Background(), mine(findStranded(context.Background()), task.JobId))

	if len(streamEntriesFor(t, task.JobId)) != 1 {
		t.Fatal("the stranded task was not republished")
	}
	if reload(t, task.JobId).PublishedAt == nil {
		t.Fatal("the republish must record PublishedAt")
	}
	if n := eventCount(t, task.JobId, "task.republished"); n != 1 {
		t.Fatalf("expected one task.republished event, found %d", n)
	}
	if strandedIDs(context.Background())[task.JobId] {
		t.Fatal("once published it must no longer be stranded")
	}
}

func TestAStrandedTaskIsRepublishedExactlyOnce(t *testing.T) {
	task := newRunnableTask(t, "fail_permanent", "n", `{}`)
	forgetEntries(t, task.JobId)
	force(t, task.JobId, map[string]interface{}{"status": "Queued", "updated_at": longAgo})

	publishStrandedTasks(context.Background(), mine(findStranded(context.Background()), task.JobId))
	// a second sweep must find nothing of ours to do
	publishStrandedTasks(context.Background(), mine(findStranded(context.Background()), task.JobId))

	if n := len(streamEntriesFor(t, task.JobId)); n != 1 {
		t.Fatalf("expected exactly one message after two sweeps, found %d", n)
	}
}

func TestFindStranded_SelectsOnlyWhatIsReallyStranded(t *testing.T) {
	now := time.Now().UTC()
	mk := func(cols map[string]interface{}) string {
		task := newRunnableTask(t, "fail_permanent", "n", `{}`)
		force(t, task.JobId, cols)
		return task.JobId
	}

	stranded := mk(map[string]interface{}{"status": "Queued", "updated_at": longAgo})
	justQueued := mk(map[string]interface{}{"status": "Queued", "updated_at": now})
	alreadyPublished := mk(map[string]interface{}{"status": "Queued", "updated_at": longAgo, "published_at": now})
	running := mk(map[string]interface{}{"status": "Running", "updated_at": longAgo})
	pending := mk(map[string]interface{}{"status": "Pending", "updated_at": longAgo})

	ids := strandedIDs(context.Background())

	if !ids[stranded] {
		t.Error("a Queued, unpublished, old task must be stranded")
	}
	if ids[justQueued] {
		t.Error("a task queued a moment ago has not had its chance to publish yet")
	}
	if ids[alreadyPublished] {
		t.Error("a task with a confirmed publish must never be republished")
	}
	if ids[running] {
		t.Error("a Running task must never be touched")
	}
	if ids[pending] {
		t.Error("a Pending task is the normal path's job, not the sweep's")
	}
}

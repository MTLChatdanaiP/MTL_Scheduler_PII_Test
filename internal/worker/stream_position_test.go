package worker

// RFC-005 §16 stream_position: a PUBLISHED message can be matched to the attempt that CLAIMED it. Real Redis, a private
// consumer group, and only this test's own messages.

import (
	"context"
	"testing"

	"MTL_Scheduler_PII_Test/internal/cache"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func eventsFor(t *testing.T, jobID, eventType string) []models.EventEnvelope {
	t.Helper()
	var rows []models.EventEnvelope
	database.DB.Where("job_id = ? AND event_type = ?", jobID, eventType).Order("id").Find(&rows)
	return rows
}

func TestPublishTask_RecordsTaskPublishedWithTheStreamIDRedisReturned(t *testing.T) {
	task := newRunnableTask(t, "fail_permanent", "n", `{}`)
	forgetEntries(t, task.JobId)

	queueAndPublish(context.Background(), []models.Task{task})

	entries := streamEntriesFor(t, task.JobId)
	if len(entries) != 1 {
		t.Fatalf("expected one stream entry, got %d", len(entries))
	}
	published := eventsFor(t, task.JobId, "task.published")
	if len(published) != 1 || published[0].StreamPosition != entries[0].ID {
		t.Fatalf("task.published must carry the id Redis assigned (%s), got %+v", entries[0].ID, published)
	}
}

func TestPublishTask_AFailedPublishWritesNoPublishedEvent(t *testing.T) {
	task := newRunnableTask(t, "fail_permanent", "n", `{}`)
	forgetEntries(t, task.JobId)
	restore := deadRedis(t)

	queueAndPublish(context.Background(), []models.Task{task})
	restore()

	if n := len(eventsFor(t, task.JobId, "task.published")); n != 0 {
		t.Fatalf("task.published asserts that Redis CONFIRMED the publish; with Redis down there must be none, found %d", n)
	}
}

func TestStreamPosition_LinksAPublishToTheAttemptThatClaimedIt(t *testing.T) {
	useTestPolicy(t, artifactPolicy())
	task := newRunnableTask(t, "fail_permanent", "n", `{}`)
	forgetEntries(t, task.JobId)
	group := privateGroup(t) // must exist BEFORE the publish: it starts at "$"

	queueAndPublish(context.Background(), []models.Task{task})
	msg := readMine(t, group, hasJob(task.JobId))
	ProcessStream(context.Background(), "test-consumer", cache.TaskStream, group, msg)

	published := eventsFor(t, task.JobId, "task.published")
	claimed := eventsFor(t, task.JobId, "attempt.claimed")
	if len(published) != 1 || len(claimed) != 1 {
		t.Fatalf("expected one published and one claimed event, got %d and %d", len(published), len(claimed))
	}
	if published[0].StreamPosition == "" || published[0].StreamPosition != claimed[0].StreamPosition || claimed[0].StreamPosition != msg.ID {
		t.Fatalf("the publish (%q) and the claim (%q) must carry the SAME stream message id %q", published[0].StreamPosition, claimed[0].StreamPosition, msg.ID)
	}
}

func TestProcessStream_EveryEventWrittenWhileHandlingADeliveryCarriesItsPosition(t *testing.T) {
	useTestPolicy(t, artifactPolicy())
	task := newRunnableTask(t, "fail_permanent", "n", `{}`)
	forgetEntries(t, task.JobId)
	group := privateGroup(t)

	if err := PublishEnvelope(context.Background(), cache.DeliveryEnvelope{JobID: task.JobId}); err != nil {
		t.Fatal(err)
	}
	msg := readMine(t, group, hasJob(task.JobId))
	ProcessStream(context.Background(), "test-consumer", cache.TaskStream, group, msg)

	for _, et := range []string{"attempt.claimed", "attempt.started", "task.started", "attempt.failed", "task.failed"} {
		rows := eventsFor(t, task.JobId, et)
		if len(rows) == 0 {
			t.Errorf("no %s event", et)
			continue
		}
		if rows[0].StreamPosition != msg.ID {
			t.Errorf("%s carries stream position %q, want %q", et, rows[0].StreamPosition, msg.ID)
		}
	}
}

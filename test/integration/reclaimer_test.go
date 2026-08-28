package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/redis/go-redis/v9"

	"MTL_Scheduler_PII_Test/internals/cache"
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
	"MTL_Scheduler_PII_Test/internals/worker"
	"MTL_Scheduler_PII_Test/test/testutil"
)

func TestReclaimer_RecoversAbandonedMessage(t *testing.T) {

	testutil.ResetAll(context.Background())

	ctx := context.Background()

	task := models.Task{
		JobId:    ulid.Make().String(),
		TaskName: "reclaim_test",
		TaskType: "dummy",
		Status:   "Pending",
	}
	database.DB.Create(&task)

	rdb := cache.Client

	// ensure the consumer group exists before claiming into it
	rdb.XGroupCreateMkStream(ctx, cache.TaskStream, worker.WorkerGroupA, "$")

	msgId, err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: cache.TaskStream,
		Values: map[string]interface{}{"job_id": task.JobId},
	}).Result()
	if err != nil {
		t.Fatalf("failed to XAdd test message: %v", err)
	}

	// claim it via a throwaway consumer, but deliberately never XAck it —
	// simulating a worker that claimed the message and then died
	_, err = rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    worker.WorkerGroupA,
		Consumer: "abandoning-test-consumer",
		Streams:  []string{cache.TaskStream, ">"},
		Count:    1,
	}).Result()
	if err != nil {
		t.Fatalf("failed to claim test message: %v", err)
	}

	t.Cleanup(func() {
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.Task{})
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.Attempt{})
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.EventEnvelope{})
		rdb.XAck(ctx, cache.TaskStream, worker.WorkerGroupA, msgId)
	})

	// wait past the reclaim idle threshold so the message is actually eligible
	time.Sleep(125 * time.Second)

	// run the reclaimer in the background, cancel it once we're done polling
	reclaimCtx, cancel := context.WithCancel(context.Background())
	go worker.StartReclaimer(reclaimCtx, "test-reclaimer")

	var updated models.Task
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		database.DB.Where("job_id = ?", task.JobId).First(&updated)
		if updated.Status == "Completed" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	cancel()

	if updated.Status != "Completed" {
		t.Errorf("expected task Status Completed after reclaim, got %s", updated.Status)
	}
}

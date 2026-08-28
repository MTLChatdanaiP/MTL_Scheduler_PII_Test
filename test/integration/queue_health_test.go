package integration_test

import (
	"context"
	"testing"

	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internals/cache"
	"MTL_Scheduler_PII_Test/internals/worker"

	"github.com/redis/go-redis/v9"
)

func TestSampleQueueHealth_ReturnsRealCounts(t *testing.T) {
	ctx := context.Background()

	testJobId := ulid.Make().String()

	rdb := cache.Client
	_, err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: cache.TaskStream,
		Values: map[string]interface{}{"job_id": testJobId},
	}).Result()
	if err != nil {
		t.Fatalf("failed to XAdd test message: %v", err)
	}

	t.Cleanup(func() {
		// no built-in "delete one stream entry" cleanup here — this message
		// will sit in the stream permanently unless manually removed via XDEL,
		// worth deciding if that matters for your test hygiene
	})

	health, err := worker.SampleQueueHealth(ctx, 100)
	if err != nil {
		t.Fatalf("SampleQueueHealth failed: %v", err)
	}

	if health.StreamLength < 1 {
		t.Errorf("expected StreamLength >= 1, got %d", health.StreamLength)
	}
}

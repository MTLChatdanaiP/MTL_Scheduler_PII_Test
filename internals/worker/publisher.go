package worker

import (
	"MTL_Scheduler_PII_Test/internals/cache"
	"MTL_Scheduler_PII_Test/internals/events"

	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// RFC-003 §5 Delivery Envelope / §6 Delivery Lifecycle: PUBLISHED -> AVAILABLE stage — XAdd makes the task available for a consumer group to claim
func PublishToStream(ctx context.Context, JobId string) {
	rdb := cache.Client

	id, err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: cache.TaskStream,
		Values: map[string]interface{}{"job_id": JobId},
	}).Result()

	if err != nil {
		if cache.IsUnavailable(err) {
			events.LogEvent(ctx, "system", "redis.unavailable", "publisher")
		}
		fmt.Println("XAdd error:", err)
		return
	}
	fmt.Printf("Added %s -> stream id %s\n", JobId, id)
}

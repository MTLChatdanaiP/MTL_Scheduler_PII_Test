package worker

import (
	"MTL_Scheduler_PII_Test/internals/cache"

	"fmt"

	"github.com/redis/go-redis/v9"
)

// RFC-003 §5 Delivery Envelope / §6 Delivery Lifecycle: PUBLISHED -> AVAILABLE stage — XAdd makes the task available for a consumer group to claim
func PublishToStream(JobId string) {
	ctx := cache.Ctx
	rdb := cache.Client

	id, err := rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: cache.TaskStream,
		Values: map[string]interface{}{"task_id": JobId},
	}).Result()

	if err != nil {
		fmt.Println("XAdd error:", err)
		return
	}
	fmt.Printf("Added %d -> stream id %s\n", JobId, id)
}

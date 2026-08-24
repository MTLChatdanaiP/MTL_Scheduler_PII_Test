package worker

import (
	"context"
	"fmt"
	"time"

	"MTL_Scheduler_PII_Test/internals/cache"
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/events"
	"MTL_Scheduler_PII_Test/internals/models"

	"github.com/redis/go-redis/v9"
)

const queuehealthInterval = 20 * time.Second //random time lol

// RFC-003 §10 Pending Delivery Monitoring / §11 Queue Health Signals: raw
// facts about the stream/consumer group, exposed for Monitoring to interpret
// — this function does not decide "backlog" or "degraded", it just reports.
func SampleQueueHealth(ctx context.Context, sampleSize int64) (models.QueueHealth, error) {
	rdb := cache.Client

	count, err := rdb.XLen(ctx, cache.TaskStream).Result()
	if err != nil {
		return models.QueueHealth{}, err
	}

	pending, err := rdb.XPending(ctx, cache.TaskStream, WorkerGroupA).Result()
	if err != nil {
		return models.QueueHealth{}, err
	}

	pendingExt, err := rdb.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: cache.TaskStream,
		Group:  WorkerGroupA,
		Start:  "-",
		End:    "+",
		Count:  sampleSize,
	}).Result()
	if err != nil {
		return models.QueueHealth{}, err
	}

	var oldestPendingAge time.Duration

	for _, p := range pendingExt {
		if p.Idle > oldestPendingAge {
			oldestPendingAge = p.Idle
		}
	}

	consumers, err := rdb.XInfoConsumers(ctx, cache.TaskStream, WorkerGroupA).Result()
	if err != nil {
		return models.QueueHealth{}, err
	}

	result := models.QueueHealth{
		StreamLength:            count,
		PendingCount:            pending.Count,
		OldestPendingAgeSeconds: int64(oldestPendingAge.Seconds()),
		ConsumerCount:           len(consumers),
		SampledAt:               time.Now(),
	}

	return result, nil
}

func StartQueueHealth(ctx context.Context) {
	for {
		select { // RFC-004 §10 Graceful Shutdown: stop starting new reclaim batches once shutdown is signaled
		case <-ctx.Done():
			return
		default:
		}

		QueueHealth, err := SampleQueueHealth(ctx, 100)
		if err != nil {
			fmt.Println("FAILED TO SAMPLE QUEUE HEALTH: ", err)
			if cache.IsUnavailable(err) {
				events.LogEvent(ctx, "system", "redis.unavailable", "queue-monitor")
			}
			time.Sleep(queuehealthInterval)
			continue
		}

		err2 := database.DB.WithContext(ctx).Create(&QueueHealth).Error
		if err2 != nil {
			fmt.Println("FAILED TO WRITE QUEUE HEALTH: ", err)
			if cache.IsUnavailable(err) {
				events.LogEvent(ctx, "system", "redis.unavailable", "queue-monitor")
			}
			time.Sleep(queuehealthInterval)
			continue
		}

		time.Sleep(queuehealthInterval)
	}
}

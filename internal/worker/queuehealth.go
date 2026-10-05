package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"MTL_Scheduler_PII_Test/internal/cache"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/events"
	"MTL_Scheduler_PII_Test/internal/models"

	"github.com/redis/go-redis/v9"
)

const (
	queuehealthInterval      = 20 * time.Second //random time lol
	degradedPendingThreshold = 20
)

var (
	queueStatusMu   sync.Mutex
	lastQueueStatus = make(map[string]string) // queueName -> "healthy" | "degraded"

	// RFC-010 §20: previous consumer count per queue, used only to detect the
	// crossing to and from zero -- never displayed anywhere itself.
	consumerCountMu   sync.Mutex
	lastConsumerCount = make(map[string]int)
)

func queueStatusFor(q models.QueueHealth) string {
	if q.PendingCount > degradedPendingThreshold {
		return "degraded"
	}
	return "healthy"
}

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
		QueueName:               cache.TaskStream,
		StreamLength:            count,
		PendingCount:            pending.Count,
		OldestPendingAgeSeconds: int64(oldestPendingAge.Seconds()),
		ConsumerCount:           len(consumers),
		SampledAt:               time.Now().UTC(),
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

		newStatus := queueStatusFor(QueueHealth)

		queueStatusMu.Lock()
		oldStatus, seen := lastQueueStatus[QueueHealth.QueueName]
		lastQueueStatus[QueueHealth.QueueName] = newStatus
		queueStatusMu.Unlock()

		if seen && oldStatus != newStatus {
			events.LogEvent(ctx, QueueHealth.QueueName, "queue."+newStatus, "queue-monitor")
		}

		// RFC-010 §20: QUEUE_NO_CONSUMER is in the critical tier. A queue with
		// zero consumers is not draining at all, which is materially different
		// from merely "degraded". Only the CROSSING is published -- a queue
		// that legitimately sits idle with no consumers produces one event,
		// not one per sample.
		consumerCountMu.Lock()
		previousConsumers, consumersSeen := lastConsumerCount[QueueHealth.QueueName]
		lastConsumerCount[QueueHealth.QueueName] = QueueHealth.ConsumerCount
		consumerCountMu.Unlock()

		if consumersSeen && previousConsumers > 0 && QueueHealth.ConsumerCount == 0 {
			events.LogEvent(ctx, QueueHealth.QueueName, "queue.no_consumer", "queue-monitor")
		}
		if consumersSeen && previousConsumers == 0 && QueueHealth.ConsumerCount > 0 {
			events.LogEvent(ctx, QueueHealth.QueueName, "queue.consumer_restored", "queue-monitor")
		}

		time.Sleep(queuehealthInterval)
	}
}

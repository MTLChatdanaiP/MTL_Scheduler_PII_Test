package worker

import (
	"context"
	"fmt"
	"time"

	"MTL_Scheduler_PII_Test/internals/cache"
	"MTL_Scheduler_PII_Test/internals/events"

	"github.com/redis/go-redis/v9"
)

// RFC-003 §12 Reclaim Semantics: "If a worker disappears while owning a pending delivery, the system may reclaim it after a configurable idle threshold."
const (
	reclaimIdleThreshold = 120 * time.Second
	reclaimInterval      = 15 * time.Second
)

// StartReclaimer periodically scans the consumer group's pending entries
// and reclaims any message that's been idle too long.
// RFC-003 §6 Delivery Lifecycle, abnormal branch: CLAIMED -> worker disappears -> pending/reclaim candidate -> REDELIVERED
// RFC-004 §14 Failure Semantics: "Process crashes... Observed facts may instead be: last execution heartbeat = old, worker heartbeat = missing, Redis pending entry = still present." XAutoClaim is how this project detects that condition
func StartReclaimer(ctx context.Context, reclaimer_id string) {
	bg_ctx := context.Background()
	rdb := cache.Client

	workerStruct := CreateWorker(reclaimer_id)
	workerInstId := workerStruct.InstanceId
	fmt.Println(workerInstId)

	for {
		select { // RFC-004 §10 Graceful Shutdown: stop starting new reclaim batches once shutdown is signaled
		case <-ctx.Done():
			return
		default:
		}
		messages, _, err := rdb.XAutoClaim(bg_ctx, &redis.XAutoClaimArgs{
			Stream:   cache.TaskStream,
			Group:    WorkerGroupA,
			Consumer: reclaimer_id,
			MinIdle:  reclaimIdleThreshold,
			Start:    "0-0",
			Count:    10,
		}).Result()
		if err != nil {
			fmt.Println("xautoclaim error:", err)
			time.Sleep(reclaimInterval)
			continue
		}

		for _, msg := range messages {
			select { // RFC-004 §10 Graceful Shutdown: stop before claiming the next message in this batch, but never interrupt a ProcessStream call already in progress
			case <-ctx.Done():
				return
			default:
			}
			JobId, ok := msg.Values["task_id"].(string)
			if !ok {
				fmt.Println("Error parsing string (RECLAIMER EVENT):", ok)
				continue
			}
			// RFC-005 §11 Lost Detection: this event is the durable evidence of a recovered/possibly-lost attempt, logged before reprocessing begins so detection time is captured accurately
			events.LogEvent(JobId, "task.recovery_started", "reclaimer")

			// Reuses the same handling path as the primary consumer (ReadStream/ProcessStream in worker.go) — RFC-004 does not distinguish reclaim processing from normal processing, only how the message was acquired
			ProcessStream(reclaimer_id, cache.TaskStream, WorkerGroupA, msg)
			fmt.Println("Results received (UNACK CLAIMED): ", msg)
		}

		time.Sleep(reclaimInterval)
	}
}

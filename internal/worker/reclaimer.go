package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"MTL_Scheduler_PII_Test/internal/cache"
	"MTL_Scheduler_PII_Test/internal/events"

	"github.com/redis/go-redis/v9"
)

// RFC-003 §12 Reclaim Semantics: "If a worker disappears while owning a pending delivery, the system may reclaim it after a configurable idle threshold."
const (
	reclaimIdleThreshold = 120 * time.Second
	reclaimInterval      = 15 * time.Second
)

// RFC-003 §6 Delivery Lifecycle, abnormal branch: CLAIMED -> worker disappears -> pending/reclaim candidate -> REDELIVERED
// RFC-004 §14 Failure Semantics: "Process crashes... Observed facts may instead be: last execution heartbeat = old, worker heartbeat = missing, Redis pending entry = still present." XAutoClaim is how this project detects that condition
func StartReclaimer(ctx context.Context, reclaimer_id string) {

	rdb := cache.Client

	workerStruct := CreateComponent(ctx, reclaimer_id, "Reclaimer")
	workerInstId := workerStruct.InstanceId
	defer MarkStopped(workerInstId) // RFC-010 §9
	markDrainingOnShutdown(ctx, workerInstId)
	fmt.Println(workerInstId)
	// a component that never reports a heartbeat shows up as UNKNOWN for ever; the reclaimer used to be one
	go StartHeartbeat(ctx, reclaimer_id, workerInstId)

	for {
		select { // RFC-004 §10 Graceful Shutdown: stop starting new reclaim batches once shutdown is signaled
		case <-ctx.Done():
			return
		default:
		}
		messages, _, err := rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream:   cache.TaskStream,
			Group:    WorkerGroupA,
			Consumer: reclaimer_id,
			MinIdle:  reclaimIdleThreshold,
			Start:    "0-0",
			Count:    10,
		}).Result()
		if err != nil {
			fmt.Println("xautoclaim error:", err)
			if cache.IsUnavailable(err) {
				events.LogEventEvery(ctx, events.RedisDownEventEvery, "system", "redis.unavailable", "reclaimer")
			}
			if cache.IsNoGroup(err) {
				_ = ensureGroup(ctx, rdb, cache.TaskStream, WorkerGroupA) // the same self-healing as ReadStream
			}
			if !sleepCtx(ctx, reclaimInterval) {
				return
			}
			continue
		}

		for _, msg := range messages {
			select { // RFC-004 §10 Graceful Shutdown: stop before claiming the next message in this batch, but never interrupt a ProcessStream call already in progress
			case <-ctx.Done():
				return
			default:
			}
			// RFC-003 §5: read through the envelope parser. A message with no usable job_id carries no
			// work, so it is dropped (acked and deleted) instead of being re-claimed forever.
			env, parseErr := cache.ParseEnvelope(msg.Values)
			if parseErr != nil {
				dropMalformed(ctx, reclaimer_id, cache.TaskStream, WorkerGroupA, msg, parseErr)
				continue
			}
			JobId := env.JobID
			// RFC-001 §6 / RFC-004 §14: only take the task over when the worker that owns its unfinished attempt
			// is really gone. If it still looks alive, leave the message pending (do NOT acknowledge it): the
			// worker will acknowledge it when it finishes, or a later pass will find it gone and recover it.
			if !prepareRecovery(ctx, JobId) {
				slog.Info("reclaim deferred: the owning worker still looks alive, leaving the message pending", "job_id", JobId)
				continue
			}

			// RFC-005 §11 Lost Detection: this event is the durable evidence of a recovered/possibly-lost attempt, logged before reprocessing begins so detection time is captured accurately
			events.LogEvent(ctx, JobId, "task.recovery_started", "reclaimer")

			// Reuses the same handling path as the primary consumer (ReadStream/ProcessStream in worker.go) — RFC-004 does not distinguish reclaim processing from normal processing, only how the message was acquired
			ProcessStream(ctx, reclaimer_id, cache.TaskStream, WorkerGroupA, msg)
			slog.Info("task reclaimed", "reclaimer_id", reclaimer_id, "job_id", JobId)
		}

		if !sleepCtx(ctx, reclaimInterval) {
			return
		}
	}
}

package worker

import (
	"context"
	"fmt"
	"time"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
	"sync/atomic"
)

// RFC-004 §6 Worker Heartbeat: "Heartbeat frequency and health thresholds are separate concepts.
// The worker only emits facts. Monitoring interprets: ONLINE / DEGRADED / OFFLINE."
const (
	HeartbeatInterval = 10 * time.Second
)

func StartHeartbeat(ctx context.Context, workerId string, instanceId string) {
	for {
		select { // RFC-004 §10 Graceful Shutdown: heartbeat goroutine now shuts down alongside
		// the others rather than being killed abruptly when main() returns
		case <-ctx.Done():
			// shutdown signaled, safe point reached — stop looping
			return
		default:
			// no shutdown yet, continue as normal
		}

		value, ok := workerCounters.Load(workerId)
		if !ok {
			continue
		}
		counter := value.(*atomic.Int64)

		WorkerHeartbeat := models.WorkerHeartbeat{WorkerId: workerId, InstanceId: instanceId, OccurredAt: time.Now(), RunningAttempts: int(counter.Load())}
		err := database.DB.WithContext(ctx).Create(&WorkerHeartbeat).Error
		if err != nil {
			fmt.Println("FAILED TO WRITE WORKER HEARTBEAT: ", err)

		}

		time.Sleep(HeartbeatInterval)
	}
}

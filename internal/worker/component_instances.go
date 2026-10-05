package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/events"
	"MTL_Scheduler_PII_Test/internal/models"
)

// RFC-010 §8/§9: per-INSTANCE component health, and §7: publishing changes to
// it live instead of only exposing it to a poll.
func GetComponentInstances(ctx context.Context) ([]models.ComponentInstance, error) {
	var workers []models.Worker
	if err := database.DB.WithContext(ctx).Order("started_at DESC").Find(&workers).Error; err != nil {
		return nil, err
	}

	var heartbeats []models.WorkerHeartbeat
	if err := database.DB.WithContext(ctx).Order("occurred_at DESC").Find(&heartbeats).Error; err != nil {
		return nil, err
	}

	// newest heartbeat per instance
	newest := make(map[string]time.Time)
	for _, hb := range heartbeats {
		if _, seen := newest[hb.InstanceId]; !seen {
			newest[hb.InstanceId] = hb.OccurredAt
		}
	}

	seen := make(map[string]bool)
	out := make([]models.ComponentInstance, 0, len(workers))
	for _, w := range workers {
		if seen[w.InstanceId] {
			continue
		}
		seen[w.InstanceId] = true

		// Rows written before ComponentType existed are plain workers, which is
		// exactly what they were, so empty falls back rather than reading UNKNOWN.
		componentType := w.ComponentType
		if componentType == "" {
			componentType = "Worker"
		}

		out = append(out, models.ComponentInstance{
			ComponentType: componentType,
			InstanceID:    w.InstanceId,
			DisplayName:   w.WorkerId,
			StartedAt:     w.StartedAt,
			LastSeenAt:    newest[w.InstanceId],
		})
	}

	return out, nil
}

var (
	instanceStatusMu   sync.Mutex
	lastInstanceStatus = make(map[string]string) // instanceId -> last verdict
)

// checkComponentInstanceTransitions is RFC-010 §7's COMPONENT resource class.
func checkComponentInstanceTransitions(ctx context.Context) error {
	instances, err := GetComponentInstances(ctx)
	if err != nil {
		fmt.Println("Failed to read component instances:", err)
		return err
	}

	now := time.Now().UTC()
	for _, inst := range instances {
		verdict := models.InstanceVerdict(inst, now, workerDegradedAfter, workerOfflineAfter)

		instanceStatusMu.Lock()
		old, previouslySeen := lastInstanceStatus[inst.InstanceID]
		lastInstanceStatus[inst.InstanceID] = verdict
		instanceStatusMu.Unlock()

		// Only a real transition is worth an event. The first sighting is not
		// a transition -- publishing then would fire a burst for every
		// instance on every backend restart.
		if previouslySeen && old != verdict {
			events.LogEvent(ctx, inst.InstanceID, "component."+lowerVerdict(verdict), "monitoring-sweep")
		}
	}

	return nil
}

// lowerVerdict takes a verdict constant and returns the lowercase suffix used
// in event type names, matching the existing worker.online / queue.degraded
// naming convention.
func lowerVerdict(v string) string {
	switch v {
	case "HEALTHY":
		return "healthy"
	case "DEGRADED":
		return "degraded"
	case "OFFLINE":
		return "offline"
	default:
		return "unknown"
	}
}

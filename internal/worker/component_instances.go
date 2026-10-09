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

	heartbeats, err := newestHeartbeatPerInstance(ctx)
	if err != nil {
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

		var stoppedAt, drainingAt time.Time
		if w.StoppedAt != nil {
			stoppedAt = *w.StoppedAt
		}
		if w.DrainingAt != nil {
			drainingAt = *w.DrainingAt
		}

		out = append(out, models.ComponentInstance{
			ComponentType: componentType,
			InstanceID:    w.InstanceId,
			DisplayName:   w.WorkerId,
			StartedAt:     w.StartedAt,
			LastSeenAt:    newest[w.InstanceId],
			BuildRevision: w.BuildRevision,
			StoppedAt:     stoppedAt,
			DrainingAt:    drainingAt,
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
		// Draining reads HEALTHY, but it is a state change worth announcing, so it is tracked as its own state.
		if inst.Draining(now) {
			verdict = "DRAINING"
		}

		instanceStatusMu.Lock()
		old, previouslySeen := lastInstanceStatus[inst.InstanceID]
		lastInstanceStatus[inst.InstanceID] = verdict
		instanceStatusMu.Unlock()

		// Only a real transition is worth an event. The first sighting is not
		// a transition -- publishing then would fire a burst for every
		// instance on every backend restart.
		if previouslySeen && old != verdict {
			events.LogEvent(ctx, inst.InstanceID, componentTransitionEvent(inst, verdict), "monitoring-sweep")
		}
	}

	return nil
}

// componentTransitionEvent names the event for an instance whose verdict just changed. A deliberate stop is "component.stopped"
// (an ordinary state change); only an unexplained OFFLINE is "component.offline", which is critical and pages someone (RFC-010 §9, §20).
func componentTransitionEvent(inst models.ComponentInstance, verdict string) string {
	if verdict == "OFFLINE" && inst.GracefullyStopped() {
		return "component.stopped"
	}
	if verdict == "DRAINING" {
		return "component.draining"
	}
	return "component." + lowerVerdict(verdict)
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

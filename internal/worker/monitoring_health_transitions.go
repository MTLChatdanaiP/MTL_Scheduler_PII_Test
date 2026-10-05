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

// RFC-010 §3/§7: monitoring-health changes must be STREAMED, not only exposed
// to whoever polls GET /monitoring/health.
//
// This watches the health of monitoring ITSELF -- how fresh each subsystem's
// own observations are -- as opposed to the health of the things monitoring
// observes, which the other checks already cover.

// subsystemDegradedAfter is how stale a subsystem's newest observation may get
// before monitoring is considered degraded at observing it.
const subsystemDegradedAfter = 2 * time.Minute

var (
	subsystemStatusMu   sync.Mutex
	lastSubsystemStatus = make(map[string]string) // subsystem name -> last verdict
)

// subsystemFreshness takes a model and the column holding its observation
// time, and returns how recently that subsystem last observed anything, plus
// whether it has ever observed anything at all.
func subsystemFreshness(ctx context.Context, model interface{}, column string) (time.Time, bool) {
	var newest *time.Time
	err := database.DB.WithContext(ctx).
		Model(model).
		Select(column).
		Order(column + " DESC").
		Limit(1).
		Scan(&newest).Error

	if err != nil || newest == nil || newest.IsZero() {
		return time.Time{}, false
	}
	return *newest, true
}

// subsystemVerdict takes a subsystem's newest observation time and whether it
// has one at all, and returns AVAILABLE, DEGRADED or UNAVAILABLE by comparing
// its age to subsystemDegradedAfter.
func subsystemVerdict(newest time.Time, ok bool) string {
	if !ok {
		return "unavailable"
	}
	if time.Since(newest) > subsystemDegradedAfter {
		return "degraded"
	}
	return "available"
}

// checkMonitoringHealthTransitions takes a context, computes the current
// verdict for each monitoring subsystem, and publishes an event only for
// subsystems whose verdict CHANGED since the previous sweep -- so a steady
// state produces no events at all, and a real degradation produces exactly
// one. Returns an error only if a subsystem could not be read.
func checkMonitoringHealthTransitions(ctx context.Context) error {
	type probe struct {
		name   string
		model  interface{}
		column string
	}

	probes := []probe{
		{"event-ingestion", &models.EventEnvelope{}, "ingested_at"},
		{"projection", &models.RunProjection{}, "last_event_at"},
		{"redis-queue-inspection", &models.QueueHealth{}, "sampled_at"},
	}

	for _, p := range probes {
		newest, ok := subsystemFreshness(ctx, p.model, p.column)
		verdict := subsystemVerdict(newest, ok)

		subsystemStatusMu.Lock()
		old, previouslySeen := lastSubsystemStatus[p.name]
		lastSubsystemStatus[p.name] = verdict
		subsystemStatusMu.Unlock()

		if previouslySeen && old != verdict {
			// "monitoring.degraded" is RFC-010 §20's MONITORING_DEGRADED
			// critical signal; the subsystem name is the event's subject so an
			// operator can tell WHICH part of monitoring changed.
			eventType := "monitoring.available"
			if verdict != "available" {
				eventType = "monitoring.degraded"
			}
			events.LogEvent(ctx, p.name, eventType, "monitoring-sweep")
			fmt.Println("monitoring subsystem transition:", p.name, old, "->", verdict)
		}
	}

	return nil
}

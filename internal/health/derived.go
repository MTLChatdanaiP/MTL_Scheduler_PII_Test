package health

import (
	"context"
	"fmt"
	"time"

	alerts "MTL_Scheduler_PII_Test/internal/alerting"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/live"
	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/worker"
)

// RFC-010 §8 / §9: the project components that are not a registered process.
//
// Workers, the scheduler and the reclaimer register themselves and heartbeat, so they are scored per instance (ComponentHealthOf).
// The API, Redis delivery, the monitoring pipeline, the PII scanner, the alert evaluator and the live gateway have no row of their
// own: their health is read from what they leave behind. That judgement used to be made in the browser (Components.svelte), which
// meant no other client, alert or stream could see it. It is made here now, once, from the same evidence and thresholds.

const (
	// activityDegradedAfter: a subsystem fed BY ACTIVITY (events, runs, task creation) with nothing new for this long is degraded --
	// but only if there is work it should have seen (see activityVerdict).
	activityDegradedAfter = 60 * time.Second
	// timerDegradedAfter / timerOfflineAfter: a subsystem that runs on a timer (the alert sweep ticks every 20 s) is judged by age.
	timerDegradedAfter = 60 * time.Second
	timerOfflineAfter  = 300 * time.Second
	// gatewayDegradedAfter / gatewayOfflineAfter: the live gateway pings each connection every 20 s.
	gatewayDegradedAfter = 90 * time.Second
	gatewayOfflineAfter  = 300 * time.Second
)

// Observation is when a subsystem last observed anything. Has is false when it never has.
type Observation struct {
	At  time.Time
	Has bool
}

// DerivedInputs is everything DerivedComponents needs, gathered by the caller so the judgement itself is a pure function.
type DerivedInputs struct {
	Now          time.Time
	Dependencies []DependencyStatus
	// QueuesWithoutConsumer are the queues whose latest sample showed no consumer.
	QueuesWithoutConsumer []string
	Ingestion             Observation // newest event ingested
	Projection            Observation // newest run projection update
	PIIScan               Observation // newest task created (every task is scanned synchronously)
	AlertSweep            Observation // when the alert evaluator last completed a sweep
	// Busy is whether anything is queued, running or due. An activity-fed subsystem is not at fault for a quiet log on an idle system.
	Busy bool
	Live live.Stats
}

func dependency(deps []DependencyStatus, name string) (DependencyStatus, bool) {
	for _, d := range deps {
		if d.Name == name {
			return d, true
		}
	}
	return DependencyStatus{}, false
}

func derived(kind, display, status, reason string, evidence map[string]interface{}) ComponentHealth {
	if evidence == nil {
		evidence = map[string]interface{}{}
	}
	return ComponentHealth{
		ComponentType: display, DisplayName: display, ComponentKind: kind,
		Health: status, Reason: reason, Status: status, StatusReason: reason, Evidence: evidence,
	}
}

func withLag(c ComponentHealth, lag, threshold time.Duration) ComponentHealth {
	l, t := lag.Milliseconds(), threshold.Milliseconds()
	c.ObservedLagMs, c.ThresholdMs = &l, &t
	return c
}

// activityVerdict judges a subsystem that only advances when work happens.
//
//	never observed anything -> UNKNOWN (no evidence is not evidence of a fault)
//	fresh                   -> HEALTHY
//	stale, nothing to do    -> HEALTHY ("idle", not a fault -- the same rule the monitoring sweep applies)
//	stale, work in flight   -> DEGRADED, never worse: quiet is not proof it stopped
func activityVerdict(label string, o Observation, now time.Time, busy bool) (status, reason string, lag time.Duration) {
	if !o.Has {
		return "UNKNOWN", fmt.Sprintf("%s: nothing observed yet", label), 0
	}
	lag = now.Sub(o.At)
	if lag <= activityDegradedAfter {
		return "HEALTHY", fmt.Sprintf("%s: last observation %s ago", label, lag.Round(time.Second)), lag
	}
	if !busy {
		return "HEALTHY", fmt.Sprintf("%s: idle, last observation %s ago (no work in flight, not a fault)", label, lag.Round(time.Second)), lag
	}
	return "DEGRADED", fmt.Sprintf("%s lag high: %s with work in flight (degraded after %s)", label, lag.Round(time.Second), activityDegradedAfter), lag
}

// DerivedComponents returns the server-judged components. Pure: the same inputs always give the same verdicts.
func DerivedComponents(in DerivedInputs) []ComponentHealth {
	var out []ComponentHealth

	// API: this report is being served by it.
	out = append(out, derived("API", "API", "HEALTHY", "answered the request that produced this report", nil))

	// QUERY_API reads Postgres; if the database does not answer, the query API cannot serve snapshots.
	if pg, ok := dependency(in.Dependencies, "postgres"); ok && pg.Status != "OK" {
		out = append(out, derived("QUERY_API", "Query API", "UNHEALTHY", "the database did not answer a ping", map[string]interface{}{"dependency": "postgres"}))
	} else {
		out = append(out, derived("QUERY_API", "Query API", "HEALTHY", "the database answered a ping", map[string]interface{}{"dependency": "postgres"}))
	}

	// REDIS_DELIVERY
	switch rd, _ := dependency(in.Dependencies, "redis"); {
	case rd.Status != "OK":
		out = append(out, derived("REDIS_DELIVERY", "Redis Delivery", "UNHEALTHY", "Redis did not answer a ping", map[string]interface{}{"dependency": "redis"}))
	case len(in.QueuesWithoutConsumer) > 0:
		out = append(out, derived("REDIS_DELIVERY", "Redis Delivery", "DEGRADED",
			fmt.Sprintf("%d queue(s) have no consumer", len(in.QueuesWithoutConsumer)),
			map[string]interface{}{"queues_without_consumer": in.QueuesWithoutConsumer}))
	default:
		out = append(out, derived("REDIS_DELIVERY", "Redis Delivery", "HEALTHY", "Redis answered and every sampled queue has a consumer", nil))
	}

	// MONITORING_INGESTOR / MONITORING_PROJECTOR
	for _, c := range []struct {
		kind, display, label string
		obs                  Observation
	}{
		{"MONITORING_INGESTOR", "Monitoring Ingestor", "event ingestion", in.Ingestion},
		{"MONITORING_PROJECTOR", "Monitoring Projector", "projection", in.Projection},
	} {
		status, reason, lag := activityVerdict(c.label, c.obs, in.Now, in.Busy)
		comp := derived(c.kind, c.display, status, reason, map[string]interface{}{"busy": in.Busy})
		if c.obs.Has {
			comp = withLag(comp, lag, activityDegradedAfter)
		}
		out = append(out, comp)
	}

	// PII_SCANNER: no active policy means it cannot scan at all; otherwise it is activity-fed like the others.
	if pol, ok := dependency(in.Dependencies, "pii_policy"); ok && pol.Status != "OK" {
		out = append(out, derived("PII_SCANNER", "PII Scanner", "UNHEALTHY", "no PII policy is active", map[string]interface{}{"dependency": "pii_policy"}))
	} else {
		status, reason, lag := activityVerdict("PII scan", in.PIIScan, in.Now, in.Busy)
		comp := derived("PII_SCANNER", "PII Scanner", status, reason, map[string]interface{}{"busy": in.Busy})
		if in.PIIScan.Has {
			comp = withLag(comp, lag, activityDegradedAfter)
		}
		out = append(out, comp)
	}

	// ALERT_EVALUATOR: ticks on a timer, so staleness here really does mean it stopped.
	if !in.AlertSweep.Has {
		out = append(out, derived("ALERT_EVALUATOR", "Alert Evaluator", "UNKNOWN", "no alert sweep has completed yet", nil))
	} else {
		age := in.Now.Sub(in.AlertSweep.At)
		switch {
		case age > timerOfflineAfter:
			out = append(out, withLag(derived("ALERT_EVALUATOR", "Alert Evaluator", "OFFLINE",
				fmt.Sprintf("last alert sweep %s ago (offline after %s)", age.Round(time.Second), timerOfflineAfter), nil), age, timerOfflineAfter))
		case age > timerDegradedAfter:
			out = append(out, withLag(derived("ALERT_EVALUATOR", "Alert Evaluator", "DEGRADED",
				fmt.Sprintf("last alert sweep %s ago (degraded after %s)", age.Round(time.Second), timerDegradedAfter), nil), age, timerDegradedAfter))
		default:
			out = append(out, withLag(derived("ALERT_EVALUATOR", "Alert Evaluator", "HEALTHY",
				fmt.Sprintf("last alert sweep %s ago", age.Round(time.Second)), nil), age, timerDegradedAfter))
		}
	}

	// REALTIME_GATEWAY: it only pings while a client is connected, so with none connected there is nothing to judge.
	ev := map[string]interface{}{"active_connections": in.Live.ActiveConnections, "live_change_backlog": in.Live.LiveChangeBacklog}
	switch {
	case in.Live.ActiveConnections == 0:
		out = append(out, derived("REALTIME_GATEWAY", "Real-Time Gateway", "HEALTHY", "no live connections (idle, not evidence of a problem)", ev))
	case in.Live.LastHeartbeatAt == nil:
		out = append(out, derived("REALTIME_GATEWAY", "Real-Time Gateway", "UNKNOWN",
			fmt.Sprintf("%d connection(s), but no heartbeat recorded yet", in.Live.ActiveConnections), ev))
	default:
		age := in.Now.Sub(*in.Live.LastHeartbeatAt)
		status, threshold := "HEALTHY", gatewayDegradedAfter
		switch {
		case age > gatewayOfflineAfter:
			status, threshold = "OFFLINE", gatewayOfflineAfter
		case age > gatewayDegradedAfter:
			status = "DEGRADED"
		}
		out = append(out, withLag(derived("REALTIME_GATEWAY", "Real-Time Gateway", status,
			fmt.Sprintf("last gateway heartbeat %s ago", age.Round(time.Second)), ev), age, threshold))
	}

	return out
}

func newestOf(ctx context.Context, model interface{}, column string) Observation {
	var newest *time.Time
	err := database.DB.WithContext(ctx).Model(model).Select(column).Order(column + " DESC").Limit(1).Scan(&newest).Error
	if err != nil || newest == nil || newest.IsZero() {
		return Observation{}
	}
	return Observation{At: *newest, Has: true}
}

// gatherDerivedInputs reads the evidence DerivedComponents judges. A read that fails becomes "nothing observed" (UNKNOWN), never a
// guess in either direction.
func gatherDerivedInputs(ctx context.Context, now time.Time) DerivedInputs {
	in := DerivedInputs{Now: now, Dependencies: CheckDependencies(ctx), Live: live.Snapshot()}

	in.Ingestion = newestOf(ctx, &models.EventEnvelope{}, "ingested_at")
	in.Projection = newestOf(ctx, &models.RunProjection{}, "last_event_at")
	in.PIIScan = newestOf(ctx, &models.Task{}, "created_at")
	if at := alerts.LastSweepAt(); !at.IsZero() {
		in.AlertSweep = Observation{At: at, Has: true}
	}
	in.Busy = worker.WorkInFlight(ctx)

	// the newest sample per queue
	var samples []models.QueueHealth
	database.DB.WithContext(ctx).Select("DISTINCT ON (queue_name) *").Order("queue_name, sampled_at DESC").Find(&samples)
	for _, q := range samples {
		if q.ConsumerCount == 0 {
			in.QueuesWithoutConsumer = append(in.QueuesWithoutConsumer, q.QueueName)
		}
	}
	return in
}

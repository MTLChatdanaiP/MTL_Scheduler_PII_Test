package health

import (
	"context"
	"testing"
	"time"

	"MTL_Scheduler_PII_Test/internal/live"
)

// RFC-010 §8/§9: the non-process components are judged by the server, from evidence, with the RFC's field names.

var now0 = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func okDeps() []DependencyStatus {
	return []DependencyStatus{{Name: "postgres", Status: "OK"}, {Name: "redis", Status: "OK"}, {Name: "pii_policy", Status: "OK"}}
}

func baseInputs() DerivedInputs {
	return DerivedInputs{
		Now: now0, Dependencies: okDeps(),
		Ingestion:  Observation{At: now0.Add(-5 * time.Second), Has: true},
		Projection: Observation{At: now0.Add(-5 * time.Second), Has: true},
		PIIScan:    Observation{At: now0.Add(-5 * time.Second), Has: true},
		AlertSweep: Observation{At: now0.Add(-5 * time.Second), Has: true},
	}
}

func byKind(t *testing.T, comps []ComponentHealth, kind string) ComponentHealth {
	t.Helper()
	for _, c := range comps {
		if c.ComponentKind == kind {
			return c
		}
	}
	t.Fatalf("no component of kind %s in %d components", kind, len(comps))
	return ComponentHealth{}
}

func TestDerived_AllRFCNonProcessKindsArePresentWithBothNamings(t *testing.T) {
	comps := DerivedComponents(baseInputs())
	for _, kind := range []string{"API", "QUERY_API", "REDIS_DELIVERY", "MONITORING_INGESTOR", "MONITORING_PROJECTOR", "PII_SCANNER", "ALERT_EVALUATOR", "REALTIME_GATEWAY"} {
		c := byKind(t, comps, kind)
		if c.Status == "" || c.Status != c.Health || c.StatusReason != c.Reason {
			t.Errorf("%s: status/status_reason must mirror health/reason, got %+v", kind, c)
		}
	}
}

func TestDerived_ProjectorLagCarriesObservedLagAndThreshold(t *testing.T) {
	in := baseInputs()
	in.Busy = true
	in.Projection = Observation{At: now0.Add(-12750 * time.Millisecond * 10), Has: true} // 127.5 s
	c := byKind(t, DerivedComponents(in), "MONITORING_PROJECTOR")

	if c.Status != "DEGRADED" {
		t.Fatalf("stale projection with work in flight is DEGRADED, got %s (%s)", c.Status, c.Reason)
	}
	if c.ObservedLagMs == nil || *c.ObservedLagMs != 127500 || c.ThresholdMs == nil || *c.ThresholdMs != 60000 {
		t.Fatalf("RFC-010 §9 example fields: observed_lag_ms/threshold_ms = %v/%v, want 127500/60000", c.ObservedLagMs, c.ThresholdMs)
	}
}

func TestDerived_IdleSystemIsNotDegradedJustQuiet(t *testing.T) {
	in := baseInputs()
	in.Busy = false
	in.Ingestion = Observation{At: now0.Add(-10 * time.Minute), Has: true}
	c := byKind(t, DerivedComponents(in), "MONITORING_INGESTOR")
	if c.Status != "HEALTHY" {
		t.Fatalf("no new events on an idle system is not a fault, got %s (%s)", c.Status, c.Reason)
	}
}

func TestDerived_ActivityFedComponentsNeverClaimOfflineAndUnobservedIsUnknown(t *testing.T) {
	in := baseInputs()
	in.Busy = true
	in.Ingestion = Observation{At: now0.Add(-24 * time.Hour), Has: true}
	in.Projection = Observation{} // never observed
	comps := DerivedComponents(in)
	if got := byKind(t, comps, "MONITORING_INGESTOR").Status; got != "DEGRADED" {
		t.Errorf("a day of silence with work in flight is DEGRADED, never OFFLINE (quiet is not proof it stopped), got %s", got)
	}
	if got := byKind(t, comps, "MONITORING_PROJECTOR").Status; got != "UNKNOWN" {
		t.Errorf("never observed is UNKNOWN, got %s", got)
	}
}

func TestDerived_RedisUnavailableIsUnhealthyAndNoConsumerIsDegraded(t *testing.T) {
	in := baseInputs()
	in.Dependencies = []DependencyStatus{{Name: "postgres", Status: "OK"}, {Name: "redis", Status: "UNAVAILABLE"}, {Name: "pii_policy", Status: "OK"}}
	if got := byKind(t, DerivedComponents(in), "REDIS_DELIVERY").Status; got != "UNHEALTHY" {
		t.Errorf("unreachable Redis is UNHEALTHY, got %s", got)
	}

	in = baseInputs()
	in.QueuesWithoutConsumer = []string{"reports"}
	c := byKind(t, DerivedComponents(in), "REDIS_DELIVERY")
	if c.Status != "DEGRADED" || c.Evidence["queues_without_consumer"] == nil {
		t.Errorf("a queue with no consumer is DEGRADED with the queue named, got %s %v", c.Status, c.Evidence)
	}
}

func TestDerived_PostgresDownMakesTheQueryAPIUnhealthy(t *testing.T) {
	in := baseInputs()
	in.Dependencies = []DependencyStatus{{Name: "postgres", Status: "UNAVAILABLE"}, {Name: "redis", Status: "OK"}, {Name: "pii_policy", Status: "OK"}}
	if got := byKind(t, DerivedComponents(in), "QUERY_API").Status; got != "UNHEALTHY" {
		t.Errorf("got %s", got)
	}
}

func TestDerived_NoActivePolicyMakesThePIIScannerUnhealthy(t *testing.T) {
	in := baseInputs()
	in.Dependencies = []DependencyStatus{{Name: "postgres", Status: "OK"}, {Name: "redis", Status: "OK"}, {Name: "pii_policy", Status: "UNAVAILABLE"}}
	c := byKind(t, DerivedComponents(in), "PII_SCANNER")
	if c.Status != "UNHEALTHY" {
		t.Errorf("a scanner with no policy cannot scan: %s (%s)", c.Status, c.Reason)
	}
}

func TestDerived_AlertEvaluatorIsJudgedByAgeBecauseItRunsOnATimer(t *testing.T) {
	cases := []struct {
		age  time.Duration
		has  bool
		want string
	}{{20 * time.Second, true, "HEALTHY"}, {90 * time.Second, true, "DEGRADED"}, {10 * time.Minute, true, "OFFLINE"}, {0, false, "UNKNOWN"}}
	for _, c := range cases {
		in := baseInputs()
		in.AlertSweep = Observation{At: now0.Add(-c.age), Has: c.has}
		if got := byKind(t, DerivedComponents(in), "ALERT_EVALUATOR").Status; got != c.want {
			t.Errorf("alert sweep %s old (has=%v): %s, want %s", c.age, c.has, got, c.want)
		}
	}
}

func TestDerived_GatewayIdleIsHealthyAndAStaleHeartbeatWithClientsIsNot(t *testing.T) {
	in := baseInputs()
	in.Live = live.Stats{ActiveConnections: 0}
	if got := byKind(t, DerivedComponents(in), "REALTIME_GATEWAY").Status; got != "HEALTHY" {
		t.Errorf("no connections is idle, not a fault: %s", got)
	}

	old := now0.Add(-200 * time.Second)
	in.Live = live.Stats{ActiveConnections: 2, LastHeartbeatAt: &old}
	if got := byKind(t, DerivedComponents(in), "REALTIME_GATEWAY").Status; got != "DEGRADED" {
		t.Errorf("clients connected but no ping for 200 s is DEGRADED: %s", got)
	}

	in.Live = live.Stats{ActiveConnections: 2}
	if got := byKind(t, DerivedComponents(in), "REALTIME_GATEWAY").Status; got != "UNKNOWN" {
		t.Errorf("clients connected and no heartbeat recorded yet is UNKNOWN: %s", got)
	}
}

func TestComponentKindFor(t *testing.T) {
	for in, want := range map[string]string{"Worker": "WORKER", "Scheduler": "SCHEDULER", "Reclaimer": "RECLAIMER", "Some Thing": "SOME_THING"} {
		if got := ComponentKindFor(in); got != want {
			t.Errorf("ComponentKindFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuild_IncludesTheDerivedComponentsFromRealEvidence(t *testing.T) {
	report, err := Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Derived) != 8 {
		t.Fatalf("expected the 8 server-judged components, got %d", len(report.Derived))
	}
	if byKind(t, report.Derived, "QUERY_API").Status != "HEALTHY" {
		t.Error("the test database answers, so the query API is healthy")
	}
	for _, c := range report.Components {
		if c.ComponentKind == "" || c.Status != c.Health {
			t.Errorf("instance %s lacks the RFC-010 fields: %+v", c.InstanceID, c)
		}
	}
}

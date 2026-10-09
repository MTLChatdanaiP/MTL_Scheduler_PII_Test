package live

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// RFC-010 §10 / §11: envelope and change types
// ---------------------------------------------------------------------------

func TestDescribe_EveryEmittedFamilyHasAResourceClass(t *testing.T) {
	cases := []struct {
		eventType, subject, wantResource, wantChange string
	}{
		{"task.created", "run-1", ResourceRun, ChangeInvalidate},
		{"task.failed", "run-1", ResourceRun, ChangeInvalidate},
		{"run.lost", "run-1", ResourceRun, ChangeInvalidate},
		{"attempt.claimed", "run-1", ResourceAttempt, ChangeInvalidate},
		{"attempt.duplicate_detected", "run-1", ResourceAttempt, ChangeInvalidate},
		{"queue.degraded", "reports", ResourceQueue, ChangeHealthChanged},
		{"queue.no_consumer", "reports", ResourceQueue, ChangeHealthChanged},
		{"queue.consumer_restored", "reports", ResourceQueue, ChangeHealthChanged},
		{"worker.offline", "w-1", ResourceWorker, ChangeHealthChanged},
		{"worker.stopped", "w-1", ResourceWorker, ChangeHealthChanged},
		{"schedule.missed", "sch-1", ResourceSchedule, ChangeInvalidate},
		{"schedule.occurrence_due", "sch-1", ResourceSchedule, ChangeInvalidate},
		{"alert.opened", "run-1", ResourceAlert, ChangeInvalidate},
		{"pii.detected", "run-1", ResourcePIIFindingSummary, ChangeInvalidate},
		{"pii.scan_completed", "run-1", ResourcePIIFindingSummary, ChangeInvalidate},
		{"pii.policy_activated", "policy", ResourcePIIPolicyActivated, ChangeInvalidate},
		{"pii.policy_reload_failed", "policy", ResourcePIIPolicyReloadFailed, ChangeInvalidate},
		{"pii.policy_drift_detected", "policy", ResourcePIIPolicyDriftDetected, ChangeInvalidate},
		{"monitoring.degraded", "projection", ResourceMonitoringHealth, ChangeHealthChanged},
		{"monitoring.available", "projection", ResourceMonitoringHealth, ChangeHealthChanged},
		{"component.offline", "inst-1", ResourceComponent, ChangeHealthChanged},
		{"component.stopped", "inst-1", ResourceComponent, ChangeHealthChanged},
		{TypePlatformSummary, "platform", ResourcePlatformSummary, ChangeInvalidate},
	}
	for _, c := range cases {
		res, id, change := Describe(c.eventType, c.subject)
		if res != c.wantResource || change != c.wantChange || id != c.subject {
			t.Errorf("Describe(%q, %q) = (%s, %s, %s), want (%s, %s, %s)", c.eventType, c.subject, res, id, change, c.wantResource, c.subject, c.wantChange)
		}
	}
}

func TestDescribe_UnknownTypeIsUnknownAndInvalidateNeverAGuess(t *testing.T) {
	res, _, change := Describe("something.new", "x")
	if res != ResourceUnknown || change != ChangeInvalidate {
		t.Fatalf("an unrecognised type must tell the client to refetch, got (%s, %s)", res, change)
	}
}

func TestEnvelope_KeepsLegacyKeysAndAddsTheRFCFields(t *testing.T) {
	at := time.Date(2026, 10, 9, 5, 0, 0, 0, time.UTC)
	env := NewEnvelope(Event{ID: 42, Type: "queue.degraded", Subject: "reports", At: at, ChainID: "chain-9", AttemptID: "att-3"}, at.Add(900*time.Millisecond), false)

	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}

	// the four keys every existing client reads
	for k, want := range map[string]interface{}{"id": float64(42), "type": "queue.degraded", "subject": "reports"} {
		if m[k] != want {
			t.Errorf("legacy key %q = %v, want %v", k, m[k], want)
		}
	}
	// RFC-010 §10
	for k, want := range map[string]interface{}{
		"live_event_id": "42", "schema_version": float64(1), "resource_type": "QUEUE", "resource_id": "reports",
		"change_type": "HEALTH_CHANGED", "change_seq": float64(42), "payload_mode": "NONE",
		"execution_chain_id": "chain-9", "attempt_id": "att-3",
	} {
		if m[k] != want {
			t.Errorf("envelope key %q = %v, want %v", k, m[k], want)
		}
	}
	if v, present := m["payload"]; !present || v != nil {
		t.Errorf("payload must be present and null, got %v (present=%v)", v, present)
	}
	fr := m["freshness"].(map[string]interface{})
	if fr["status"] != "FRESH" || fr["projection_lag_ms"] != float64(900) {
		t.Errorf("freshness = %v, want FRESH / 900", fr)
	}
}

func TestEnvelope_FreshnessStatuses(t *testing.T) {
	at := time.Now().UTC()
	if got := NewEnvelope(Event{ID: 1, Type: "task.created", At: at}, at.Add(10*time.Second), false).Freshness.Status; got != "LAGGING" {
		t.Errorf("a 10 s old event published live is LAGGING, got %s", got)
	}
	if got := NewEnvelope(Event{ID: 1, Type: "task.created", At: at}, at.Add(10*time.Second), true).Freshness.Status; got != "REPLAYED" {
		t.Errorf("a replayed event is REPLAYED whatever its age, got %s", got)
	}
	// clock skew: observed in the future must not produce a negative lag
	if got := NewEnvelope(Event{ID: 1, Type: "task.created", At: at.Add(time.Minute)}, at, false).Freshness.ProjectionLagMs; got != 0 {
		t.Errorf("negative lag must clamp to 0, got %d", got)
	}
}

func TestEnvelope_SummaryHasNoLiveEventID(t *testing.T) {
	env := NewEnvelope(SummaryEvent(), time.Now(), false)
	if env.ID != 0 || env.LiveEventID != "" || env.ResourceType != ResourcePlatformSummary {
		t.Fatalf("a synthetic summary has id 0 and no live_event_id, got %+v", env)
	}
}

func TestResyncFrame_CarriesTheRequiredChangeType(t *testing.T) {
	raw, _ := json.Marshal(NewResyncFrame())
	if !strings.Contains(string(raw), `"change_type":"RESYNC_REQUIRED"`) || !strings.Contains(string(raw), `"reason":"REPLAY_GAP"`) {
		t.Fatalf("resync body = %s", raw)
	}
}

// ---------------------------------------------------------------------------
// RFC-010 §15: scopes
// ---------------------------------------------------------------------------

func TestParseScope_Vocabulary(t *testing.T) {
	good := []string{"platform.summary", "runs", "run:r1", "execution-chain:c1", "queues", "queue:orders", "workers", "worker:w1",
		"schedules", "alerts", "pii.summary", "monitoring.health", "components"}
	for _, g := range good {
		sc, err := ParseScope(g)
		if err != nil || sc.String() != g {
			t.Errorf("ParseScope(%q) = %v, %v; want it accepted unchanged", g, sc, err)
		}
	}

	bad := []string{"", "bogus", "run", "run:", "queue:", "runs:r1", "platform.summary:x", "RUNS", "worker:" + strings.Repeat("x", 201)}
	for _, b := range bad {
		if _, err := ParseScope(b); err == nil {
			t.Errorf("ParseScope(%q) should be refused", b)
		} else if _, ok := err.(*ScopeError); !ok {
			t.Errorf("ParseScope(%q) error should be a *ScopeError, got %T", b, err)
		}
	}
}

func TestParseScopes_DedupesAndBoundsTheCount(t *testing.T) {
	got, err := ParseScopes([]string{"workers", "queues", "workers"})
	if err != nil || len(got) != 2 {
		t.Fatalf("duplicates should collapse, got %v, %v", got, err)
	}

	var many []string
	for i := 0; i < MaxScopes+1; i++ {
		many = append(many, fmt.Sprintf("worker:w%d", i))
	}
	if _, err := ParseScopes(many); err == nil {
		t.Fatalf("%d distinct scopes must be refused", MaxScopes+1)
	}
	if _, err := ParseScopes(many[:MaxScopes]); err != nil {
		t.Fatalf("exactly %d scopes is allowed: %v", MaxScopes, err)
	}
}

func TestScope_Matches(t *testing.T) {
	cases := []struct {
		scope string
		ev    Event
		want  bool
	}{
		{"workers", Event{Type: "worker.offline", Subject: "w1"}, true},
		{"workers", Event{Type: "queue.degraded", Subject: "w1"}, false},
		{"worker:w1", Event{Type: "worker.offline", Subject: "w1"}, true},
		{"worker:w1", Event{Type: "worker.offline", Subject: "w2"}, false},
		{"queue:orders", Event{Type: "queue.no_consumer", Subject: "orders"}, true},
		{"queue:orders", Event{Type: "queue.no_consumer", Subject: "other"}, false},
		{"runs", Event{Type: "task.created", Subject: "r1"}, true},
		{"runs", Event{Type: "attempt.started", Subject: "r1"}, true},
		{"runs", Event{Type: "run.lost", Subject: "r1"}, true},
		{"runs", Event{Type: "alert.opened", Subject: "r1"}, false},
		{"run:r1", Event{Type: "attempt.started", Subject: "r1"}, true},
		{"run:r1", Event{Type: "attempt.started", Subject: "r2"}, false},
		{"execution-chain:c1", Event{Type: "alert.opened", Subject: "x", ChainID: "c1"}, true}, // by chain, whatever the type
		{"execution-chain:c1", Event{Type: "task.created", Subject: "c1", ChainID: "c2"}, false},
		{"execution-chain:c1", Event{Type: "task.created", Subject: "c1"}, false}, // no chain id on the event: no match
		{"alerts", Event{Type: "alert.updated"}, true},
		{"pii.summary", Event{Type: "pii.detected"}, true},
		{"monitoring.health", Event{Type: "monitoring.degraded"}, true},
		{"components", Event{Type: "component.healthy"}, true},
		{"schedules", Event{Type: "schedule.missed"}, true},
		{"platform.summary", Event{Type: TypePlatformSummary}, true},
		{"platform.summary", Event{Type: "task.created"}, false},
		{"runs", Event{Type: TypePlatformSummary}, false}, // no other scope ever receives the synthetic event
	}
	for _, c := range cases {
		sc, err := ParseScope(c.scope)
		if err != nil {
			t.Fatal(err)
		}
		if got := sc.Matches(c.ev); got != c.want {
			t.Errorf("scope %q on %+v = %v, want %v", c.scope, c.ev, got, c.want)
		}
	}
}

func TestScope_Permissions(t *testing.T) {
	want := map[string]string{"alerts": "alerts.read", "pii.summary": "pii.findings.read", "workers": "", "queue:q": "", "components": "", "platform.summary": ""}
	for raw, perm := range want {
		sc, _ := ParseScope(raw)
		if got := sc.Permission(); got != perm {
			t.Errorf("Permission(%q) = %q, want %q", raw, got, perm)
		}
	}
	scopes, _ := ParseScopes([]string{"alerts", "pii.summary", "alerts", "workers"})
	if got := (Subscription{Scopes: scopes}).Permissions(); len(got) != 2 {
		t.Errorf("distinct permissions = %v, want 2", got)
	}
}

func TestSubscription_ReplaySQL(t *testing.T) {
	if _, _, ok := (Subscription{}).ReplaySQL(); ok {
		t.Error("an empty subscription has no SQL of its own (the handler replays everything it is allowed to)")
	}
	summaryOnly, _ := ParseScopes([]string{"platform.summary"})
	if _, _, ok := (Subscription{Scopes: summaryOnly}).ReplaySQL(); ok {
		t.Error("platform.summary has no stored rows, so no replay SQL")
	}

	scopes, _ := ParseScopes([]string{"queue:orders", "execution-chain:c1", "platform.summary"})
	cond, args, ok := (Subscription{Scopes: scopes}).ReplaySQL()
	if !ok {
		t.Fatal("expected SQL")
	}
	if !strings.Contains(cond, "event_type LIKE ?") || !strings.Contains(cond, "job_id = ?") || !strings.Contains(cond, "execution_chain_id = ?") {
		t.Errorf("cond = %s", cond)
	}
	if strings.Count(cond, "?") != len(args) {
		t.Errorf("%d placeholders but %d args", strings.Count(cond, "?"), len(args))
	}
	if strings.Contains(cond, "orders") || strings.Contains(cond, "c1") {
		t.Errorf("values must be bound, never inlined: %s", cond)
	}
}

func TestHub_ScopedClientOnlyReceivesItsScopes(t *testing.T) {
	hub := NewHub()
	scopes, _ := ParseScopes([]string{"queue:orders", "workers"})
	scoped := hub.SubscribeMatching(Subscription{Scopes: scopes}.Match(), false)
	all := hub.Subscribe()
	defer hub.Unsubscribe(scoped)
	defer hub.Unsubscribe(all)

	hub.Publish(Event{ID: 1, Type: "queue.degraded", Subject: "orders"})
	hub.Publish(Event{ID: 2, Type: "queue.degraded", Subject: "other"})
	hub.Publish(Event{ID: 3, Type: "worker.offline", Subject: "w1"})
	hub.Publish(Event{ID: 4, Type: "task.created", Subject: "r1"})

	if len(scoped) != 2 {
		t.Fatalf("scoped client should get exactly queue:orders + workers, got %d", len(scoped))
	}
	if len(all) != 4 {
		t.Fatalf("an unscoped client is unchanged and gets everything, got %d", len(all))
	}
}

// ---------------------------------------------------------------------------
// RFC-010 §7: platform.summary
// ---------------------------------------------------------------------------

func TestSummary_OnlyOptedInClientsReceiveIt(t *testing.T) {
	hub := NewHub()
	want := hub.SubscribeMatching(Subscription{Scopes: []Scope{{Kind: kindPlatformSummary}}}.Match(), true)
	legacy := hub.Subscribe()
	scopedOther := hub.SubscribeMatching(Subscription{Scopes: []Scope{{Kind: kindRuns}}}.Match(), false)
	defer hub.Unsubscribe(want)
	defer hub.Unsubscribe(legacy)
	defer hub.Unsubscribe(scopedOther)

	hub.PublishSummary(SummaryEvent())

	if len(want) != 1 {
		t.Fatalf("the summary subscriber must get it, got %d", len(want))
	}
	if len(legacy) != 0 || len(scopedOther) != 0 {
		t.Fatalf("nobody else may receive it (legacy=%d, other=%d)", len(legacy), len(scopedOther))
	}
}

func TestSummary_IsNotCountedAsPublishedSoItCannotHideARealBacklog(t *testing.T) {
	hub := NewHub()
	ch := hub.SubscribeMatching(nil, true)
	defer hub.Unsubscribe(ch)

	before := publishedTotal.Load()
	for i := 0; i < 5; i++ {
		hub.PublishSummary(SummaryEvent())
	}
	if got := publishedTotal.Load(); got != before {
		t.Fatalf("summary events must not count as published (%d -> %d): Backlog = recorded - published would read 0 while real events are stuck", before, got)
	}
}

func TestSummary_NeverCutsASlowClient(t *testing.T) {
	hub := NewHub()
	ch := hub.SubscribeMatching(nil, true)
	for i := 0; i < cap(ch); i++ {
		ch <- Event{ID: uint(i + 1), Type: "task.created", Subject: "fill"}
	}
	droppedBefore := informationalDropped.Load()
	hub.PublishSummary(SummaryEvent())

	if hub.Stats().ActiveConnections != 1 {
		t.Fatal("a full buffer must not cost the client its connection over a summary")
	}
	if informationalDropped.Load() != droppedBefore+1 {
		t.Fatal("the dropped summary should be counted as an informational drop")
	}
}

func TestSummaryNotifier_ABurstProducesOneSummaryAfterTheLastChange(t *testing.T) {
	hub := NewHub()
	ch := hub.SubscribeMatching(nil, true)
	defer hub.Unsubscribe(ch)
	n := newSummaryNotifier(hub, 60*time.Millisecond)

	for i := 0; i < 50; i++ {
		n.notify()
	}
	if len(ch) != 0 {
		t.Fatal("nothing may be sent before the window ends")
	}
	time.Sleep(200 * time.Millisecond)
	if len(ch) != 1 {
		t.Fatalf("50 changes in one window must produce exactly 1 summary, got %d", len(ch))
	}
}

func TestSummaryNotifier_AChangeAfterTheWindowArmsTheNextOne(t *testing.T) {
	hub := NewHub()
	ch := hub.SubscribeMatching(nil, true)
	defer hub.Unsubscribe(ch)
	n := newSummaryNotifier(hub, 40*time.Millisecond)

	n.notify()
	time.Sleep(120 * time.Millisecond)
	n.notify() // a change after the first summary went out must not be swallowed
	time.Sleep(120 * time.Millisecond)

	if len(ch) != 2 {
		t.Fatalf("two separated changes -> two summaries, got %d (a leading-edge window would lose the second)", len(ch))
	}
}

func TestSummaryNotifier_ConcurrentNotifiesAreSafe(t *testing.T) {
	hub := NewHub()
	ch := hub.SubscribeMatching(nil, true)
	defer hub.Unsubscribe(ch)
	n := newSummaryNotifier(hub, 20*time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				n.notify()
			}
		}()
	}
	wg.Wait()
	time.Sleep(100 * time.Millisecond)
	if len(ch) < 1 {
		t.Fatal("at least one summary expected")
	}
}

// ---------------------------------------------------------------------------
// RFC-010 §20: priority
// ---------------------------------------------------------------------------

func TestPriorityOfEvent_AlertSeverity(t *testing.T) {
	cases := []struct {
		sev  string
		want EventPriority
	}{
		{"CRITICAL", PriorityCritical},
		{"WARNING", PriorityNormal},
		{"INFO", PriorityNormal},
		{"", PriorityCritical}, // severity unknown (a replayed row): rank HIGH, not low
	}
	for _, c := range cases {
		if got := PriorityOfEvent(Event{Type: "alert.opened", Severity: c.sev}); got != c.want {
			t.Errorf("alert.opened severity %q = %v, want %v", c.sev, got, c.want)
		}
	}
}

func TestPriority_CompleteCriticalListAndStoppedIsNotCritical(t *testing.T) {
	if PriorityOf("pii.policy_violated") != PriorityCritical {
		t.Error("PII_POLICY_VIOLATED is in the RFC's critical list")
	}
	for _, normal := range []string{"component.stopped", "worker.stopped", "queue.consumer_restored", "monitoring.available"} {
		if PriorityOf(normal) != PriorityNormal {
			t.Errorf("%s is an ordinary state change, not critical", normal)
		}
	}
	if PriorityOf("component.offline") != PriorityCritical {
		t.Error("an unexplained offline component stays critical")
	}
}

func TestSlowClient_WarningAlertStillCutsRatherThanDrops(t *testing.T) {
	// A WARNING alert is NORMAL: a fact. It must never be silently dropped, so a full buffer cuts the client.
	hub := NewHub()
	ch := hub.Subscribe()
	for i := 0; i < cap(ch); i++ {
		ch <- Event{ID: uint(i + 1), Type: "task.created", Subject: fmt.Sprintf("fill-%d", i)}
	}
	cutBefore := slowCutTotal.Load()
	hub.Publish(Event{ID: 9999, Type: "alert.opened", Subject: "r1", Severity: "WARNING"})
	if slowCutTotal.Load() != cutBefore+1 {
		t.Fatal("a NORMAL event that cannot be delivered must cut the client")
	}
}

// ---------------------------------------------------------------------------
// RFC-010 §19: connection limits
// ---------------------------------------------------------------------------

func TestConnLimiter_CapsPerCallerAndFreesSlots(t *testing.T) {
	l := NewConnLimiter()
	rejectedBefore := rejectedSubscriptions.Load()

	if !l.Acquire("a", 2) || !l.Acquire("a", 2) {
		t.Fatal("the first two connections are allowed")
	}
	if l.Acquire("a", 2) {
		t.Fatal("the third connection from the same caller must be refused")
	}
	if rejectedSubscriptions.Load() != rejectedBefore+1 {
		t.Fatal("a refusal must be counted")
	}
	if !l.Acquire("b", 2) {
		t.Fatal("another caller is unaffected")
	}

	l.Release("a")
	if !l.Acquire("a", 2) {
		t.Fatal("closing a connection frees its slot")
	}
}

func TestConnLimiter_DoubleReleaseNeverGoesNegative(t *testing.T) {
	l := NewConnLimiter()
	l.Acquire("a", 1)
	l.Release("a")
	l.Release("a")
	l.Release("a")
	if l.Open("a") != 0 {
		t.Fatalf("open = %d", l.Open("a"))
	}
	if !l.Acquire("a", 1) || l.Acquire("a", 1) {
		t.Fatal("after extra releases the cap must still be exactly 1")
	}
}

func TestMaxConnectionsPerKey_ReadsTheEnvironmentWhenCalled(t *testing.T) {
	t.Setenv("LIVE_MAX_CONNECTIONS_PER_KEY", "3")
	if MaxConnectionsPerKey() != 3 {
		t.Fatal("a valid value is used")
	}
	for _, bad := range []string{"0", "-2", "abc", ""} {
		t.Setenv("LIVE_MAX_CONNECTIONS_PER_KEY", bad)
		if MaxConnectionsPerKey() != DefaultMaxConnectionsPerKey {
			t.Errorf("%q must fall back to the default", bad)
		}
	}
}

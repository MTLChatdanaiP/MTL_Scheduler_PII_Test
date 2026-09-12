package integration_test

// RFC-007 §8 — metric resolvers.
//
// Integration rather than unit because resolvers query the database. The
// resolver is unexported, so this calls it through the exported map that the
// sweep also uses -- which has the side benefit of testing the map wiring too:
// a resolver missing from metricResolvers fails here rather than at runtime.

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	alerts "MTL_Scheduler_PII_Test/internals/alerting"
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
)

// seedHeartbeat writes one heartbeat row aged by the given duration, and
// registers cleanup. Age is expressed as "how long ago" rather than an absolute
// time so the tests read the way the metric does.
func seedHeartbeat(t *testing.T, workerID string, ago time.Duration, running int, capacity int) models.WorkerHeartbeat {
	t.Helper()

	hb := models.WorkerHeartbeat{
		WorkerId:        workerID,
		InstanceId:      "test-instance-" + ulid.Make().String(),
		OccurredAt:      time.Now().UTC().Add(-ago),
		RunningAttempts: running,
		Capacity:        capacity,
	}

	if err := database.DB.Create(&hb).Error; err != nil {
		t.Fatalf("failed to seed heartbeat: %v", err)
	}

	t.Cleanup(func() {
		database.DB.Where("worker_id = ?", workerID).Delete(&models.WorkerHeartbeat{})
	})

	return hb
}

// resolveHeartbeatAge calls the resolver through the metric map, the same way
// the sweep does.
func resolveHeartbeatAge(t *testing.T) []alerts.MetricSample {
	t.Helper()

	resolver, ok := alerts.MetricResolvers["worker.heartbeat_age"]
	if !ok {
		t.Fatal("worker.heartbeat_age is not registered in MetricResolvers -- a rule naming it would load fine and never fire")
	}

	samples, err := resolver(context.Background())
	if err != nil {
		t.Fatalf("resolver returned an error: %v", err)
	}
	return samples
}

// sampleFor picks out the sample for one worker, so a test is not affected by
// heartbeats other tests or a running app happen to have left behind.
func sampleFor(t *testing.T, samples []alerts.MetricSample, workerID string) alerts.MetricSample {
	t.Helper()

	var found []alerts.MetricSample
	for _, s := range samples {
		if s.SubjectID == workerID {
			found = append(found, s)
		}
	}

	if len(found) == 0 {
		t.Fatalf("no sample for worker %q", workerID)
	}

	// THE TIME-SERIES TRAP. Heartbeats accumulate one row every few seconds
	// forever, so a resolver that does not reduce to the newest row per worker
	// returns one sample per ROW. That would open an alert against a heartbeat
	// from an hour ago for a worker that is perfectly healthy right now -- and
	// the resulting alert looks entirely genuine.
	if len(found) > 1 {
		t.Fatalf("worker %q produced %d samples, want exactly 1 -- the resolver is returning every row instead of the newest per worker", workerID, len(found))
	}

	return found[0]
}

func TestMetric_HeartbeatAge_UsesNewestRowPerWorker(t *testing.T) {
	workerID := "test-worker-" + ulid.Make().String()

	// Deliberately seeded oldest-first, so a resolver that happens to return
	// whatever the database hands back cannot pass by accident.
	seedHeartbeat(t, workerID, 50*time.Minute, 0, 5)
	seedHeartbeat(t, workerID, 10*time.Minute, 2, 5)
	seedHeartbeat(t, workerID, 30*time.Second, 1, 5)

	sample := sampleFor(t, resolveHeartbeatAge(t), workerID)

	// Should reflect the 30-second row, not the 50-minute one. Generous bounds
	// so the test does not flake on a slow machine.
	if sample.Numeric < 25 || sample.Numeric > 120 {
		t.Errorf("age = %v seconds, want roughly 30 -- the resolver picked an older row", sample.Numeric)
	}

	// Evidence must come from the SAME row the age came from. A resolver that
	// reduced correctly but built evidence from a different row would produce an
	// alert whose explanation contradicts its own number.
	if running, ok := sample.Evidence["running_attempts"]; ok {
		if asInt, isInt := running.(int); isInt && asInt != 1 {
			t.Errorf("evidence running_attempts = %v, want 1 (from the newest row)", running)
		}
	}
}

func TestMetric_HeartbeatAge_OneSamplePerWorker(t *testing.T) {
	workerA := "test-worker-a-" + ulid.Make().String()
	workerB := "test-worker-b-" + ulid.Make().String()

	seedHeartbeat(t, workerA, 2*time.Minute, 0, 5)
	seedHeartbeat(t, workerA, 20*time.Second, 1, 5)
	seedHeartbeat(t, workerB, 5*time.Minute, 3, 10)

	samples := resolveHeartbeatAge(t)

	a := sampleFor(t, samples, workerA)
	b := sampleFor(t, samples, workerB)

	if a.Numeric > 90 {
		t.Errorf("worker A age = %v, want roughly 20 seconds", a.Numeric)
	}
	if b.Numeric < 250 {
		t.Errorf("worker B age = %v, want roughly 300 seconds", b.Numeric)
	}

	// Subject vocabulary: metric resolvers emit RFC-007 §7 terms directly. There
	// is no translation step here -- that only exists on the annotation path,
	// because only monitoring speaks the other vocabulary.
	if a.SubjectType != "WORKER" {
		t.Errorf("subject type = %q, want WORKER", a.SubjectType)
	}

	// A worker has no meaningful scope attribute, so JOB_TYPE and PII_CATEGORY
	// scoped rules must not match worker samples.
	if a.ScopeValue != "" {
		t.Errorf("scope value = %q, want empty for a worker sample", a.ScopeValue)
	}
}

func TestMetric_HeartbeatAge_TextMatchesNumeric(t *testing.T) {
	workerID := "test-worker-" + ulid.Make().String()
	seedHeartbeat(t, workerID, 90*time.Second, 0, 5)

	sample := sampleFor(t, resolveHeartbeatAge(t), workerID)

	if sample.Text == "" {
		t.Fatal("Text is empty -- an EQ/NEQ rule would compare its textValue against \"\" and silently never match")
	}

	parsed, err := strconv.ParseFloat(sample.Text, 64)
	if err != nil {
		t.Fatalf("Text %q does not parse as a number: %v", sample.Text, err)
	}

	// Text and Numeric must describe the same measurement. They are compared by
	// different operators -- EQ/NEQ read Text, GT/LT read Numeric -- so a rules
	// file could reasonably use either against the same metric.
	if parsed != sample.Numeric {
		t.Errorf("Text %q parses to %v but Numeric is %v -- formatting is losing precision", sample.Text, parsed, sample.Numeric)
	}
}

func TestMetric_HeartbeatAge_IgnoresRowsOutsideTheWindow(t *testing.T) {
	workerID := "test-worker-ancient-" + ulid.Make().String()

	// A decommissioned worker: its newest heartbeat is far older than the query
	// window. Without the bound it would report an age of millions of seconds
	// and satisfy every heartbeat_age rule forever, for a machine nobody expects
	// to be running.
	seedHeartbeat(t, workerID, 25*time.Hour, 0, 5)

	for _, s := range resolveHeartbeatAge(t) {
		if s.SubjectID == workerID {
			t.Errorf("a heartbeat older than the query window produced a sample (age %v seconds)", s.Numeric)
		}
	}

	// NOTE the consequence, which is deliberate but easy to forget: a worker
	// gone longer than the window produces no sample rather than a breaching
	// one, so it stops alerting instead of alerting harder. That interacts
	// directly with metric-alert resolution -- "no sample" must not be treated
	// as "no longer breaching", or such an alert would be closed at exactly the
	// wrong moment.
}

func TestMetric_HeartbeatAge_NoHeartbeatsIsNotAnError(t *testing.T) {
	// "No workers have reported" is a valid state, not a failure. A resolver
	// that returned an error here would make the whole metric sweep log a
	// failure every tick on an idle system.
	resolver, ok := alerts.MetricResolvers["worker.heartbeat_age"]
	if !ok {
		t.Fatal("worker.heartbeat_age is not registered")
	}

	samples, err := resolver(context.Background())
	if err != nil {
		t.Fatalf("resolver errored: %v", err)
	}
	if samples == nil {
		t.Error("resolver returned a nil slice -- prefer an empty one so callers can range without a nil check")
	}
}

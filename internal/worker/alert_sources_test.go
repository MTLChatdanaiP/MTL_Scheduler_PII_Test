package worker

// RFC-007 §4: the five alert types that had no data source. These run the whole chain against the real
// database: a real event in the log, then the real resolver reading it.
//
// They only READ the shared event log and tasks tables (plus rows they create and remove themselves).

import (
	"context"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	alerts "MTL_Scheduler_PII_Test/internal/alerting"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

const alertTestProducer = "test-alert-sources"

func seedEvent(t *testing.T, subject, eventType string, at time.Time) {
	t.Helper()
	e := models.EventEnvelope{
		JobId: subject, EventID: ulid.Make().String(), EventType: eventType, OccurredAt: at, IngestedAt: at,
		Producer: alertTestProducer, SchemaVersion: "1",
	}
	if err := database.DB.Create(&e).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		database.DB.Unscoped().Where("job_id = ? AND producer = ?", subject, alertTestProducer).Delete(&models.EventEnvelope{})
	})
}

func sampleFor(t *testing.T, metric, subject string) (alerts.MetricSample, bool) {
	t.Helper()
	resolver := alerts.MetricResolvers[metric]
	if resolver == nil {
		t.Fatalf("metric %q is not registered", metric)
	}
	samples, err := resolver(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range samples {
		if s.SubjectID == subject {
			return s, true
		}
	}
	return alerts.MetricSample{}, false
}

var eventMetrics = map[string]string{
	"run.failed_age":            "task.failed",
	"run.retries_exhausted_age": "task.retries_exhausted",
	"run.timed_out_age":         "task.timed_out",
	"pii.scan_failed_age":       "pii.scan_failed",
}

func TestEventAgeMetrics_ReportHowLongAgoTheEventHappened(t *testing.T) {
	for metric, eventType := range eventMetrics {
		t.Run(metric, func(t *testing.T) {
			subject := "alert-src-" + ulid.Make().String()
			seedEvent(t, subject, eventType, time.Now().UTC().Add(-120*time.Second))

			s, ok := sampleFor(t, metric, subject)
			if !ok {
				t.Fatalf("%s did not report an event of type %s", metric, eventType)
			}
			if s.SubjectType != "RUN" {
				t.Errorf("subject type = %q, want RUN", s.SubjectType)
			}
			if s.Numeric < 119 || s.Numeric > 140 {
				t.Errorf("age = %.1fs, want about 120", s.Numeric)
			}
			if s.Evidence["event_id"] == "" || s.Evidence["event_type"] != eventType {
				t.Errorf("evidence should name the event: %v", s.Evidence)
			}
		})
	}
}

func TestEventAgeMetrics_EachReadsOnlyItsOwnEventType(t *testing.T) {
	subject := "alert-src-" + ulid.Make().String()
	seedEvent(t, subject, "task.failed", time.Now().UTC().Add(-30*time.Second))

	if _, ok := sampleFor(t, "run.failed_age", subject); !ok {
		t.Fatal("run.failed_age must see a task.failed event")
	}
	for _, other := range []string{"run.retries_exhausted_age", "run.timed_out_age", "pii.scan_failed_age"} {
		if _, ok := sampleFor(t, other, subject); ok {
			t.Errorf("%s reported a task.failed event: each metric must read only its own event type", other)
		}
	}
}

func TestEventAgeMetric_ReportsTheNewestEventPerSubject(t *testing.T) {
	subject := "alert-src-" + ulid.Make().String()
	seedEvent(t, subject, "task.failed", time.Now().UTC().Add(-10*time.Minute))
	seedEvent(t, subject, "task.failed", time.Now().UTC().Add(-30*time.Second))

	samples, _ := alerts.MetricResolvers["run.failed_age"](context.Background())
	var mine []alerts.MetricSample
	for _, s := range samples {
		if s.SubjectID == subject {
			mine = append(mine, s)
		}
	}
	if len(mine) != 1 {
		t.Fatalf("one sample per subject, got %d", len(mine))
	}
	if mine[0].Numeric > 60 {
		t.Fatalf("the NEWEST event should win (about 30s), got %.0fs", mine[0].Numeric)
	}
}

func TestEventAgeMetric_IgnoresEventsOlderThanADay(t *testing.T) {
	subject := "alert-src-" + ulid.Make().String()
	seedEvent(t, subject, "task.failed", time.Now().UTC().Add(-25*time.Hour))

	if _, ok := sampleFor(t, "run.failed_age", subject); ok {
		t.Fatal("an event from 25 hours ago is outside the window and must not be reported")
	}
}

func TestScheduleCreationLateness_ReportsTheWorstGapPerSchedule(t *testing.T) {
	scheduleID := "lateness-" + ulid.Make().String()
	now := time.Now().UTC()

	mk := func(schedule string, expectedAgo time.Duration, createdAgo time.Duration) {
		task := newRunnableTask(t, "fail_permanent", "n", `{}`)
		database.DB.Model(&models.Task{}).Where("job_id = ?", task.JobId).UpdateColumns(map[string]interface{}{
			"schedule_id": schedule, "expected_at": now.Add(-expectedAgo), "created_at": now.Add(-createdAgo),
		})
	}
	mk(scheduleID, 100*time.Second, 0)            // created 100s after it was expected
	mk(scheduleID, 10*time.Second, 0)             // created 10s after
	mk("", 500*time.Second, 0)                    // not a scheduled task: ignored
	mk(scheduleID, 4000*time.Second, 2*time.Hour) // created more than an hour ago: ignored

	s, ok := sampleFor(t, "schedule.creation_lateness", scheduleID)
	if !ok {
		t.Fatal("a schedule that produced runs must be reported")
	}
	if s.SubjectType != "SCHEDULE" {
		t.Errorf("subject type = %q, want SCHEDULE", s.SubjectType)
	}
	if s.Numeric < 99 || s.Numeric > 101 {
		t.Fatalf("the worst gap among the in-window runs is 100s, got %.1f", s.Numeric)
	}
}

// ---------------------------------------------------------------------------
// The events the alerts depend on really are emitted, end to end
// ---------------------------------------------------------------------------

func TestProcessTask_ATimeoutEmitsTaskTimedOutAndTheAlertSourceSeesIt(t *testing.T) {
	useTestPolicy(t, artifactPolicy())
	task := newRunnableTask(t, "fail_timeout", "n", `{}`)

	ProcessTask(context.Background(), task.JobId, "alert-src-worker")

	if n := eventCount(t, task.JobId, "task.timed_out"); n != 1 {
		t.Fatalf("a handler that reports a timeout must emit task.timed_out, found %d", n)
	}
	if _, ok := sampleFor(t, "run.timed_out_age", task.JobId); !ok {
		t.Fatal("and run.timed_out_age must report it: RUN_TIMEOUT is reachable")
	}
}

func TestProcessTask_OtherFailuresDoNotEmitTaskTimedOut(t *testing.T) {
	useTestPolicy(t, artifactPolicy())
	for _, taskType := range []string{"fail_permanent", "fail_invalid_input", "fail_dependency", "fail_infrastructure"} {
		task := newRunnableTask(t, taskType, "n", `{}`)
		ProcessTask(context.Background(), task.JobId, "alert-src-worker")
		if n := eventCount(t, task.JobId, "task.timed_out"); n != 0 {
			t.Errorf("%s must not be reported as a timeout, found %d events", taskType, n)
		}
	}
}

func TestProcessTask_AFailedRunIsReachableAsRunFailed(t *testing.T) {
	useTestPolicy(t, artifactPolicy())
	task := newRunnableTask(t, "fail_permanent", "n", `{}`)

	ProcessTask(context.Background(), task.JobId, "alert-src-worker")

	if _, ok := sampleFor(t, "run.failed_age", task.JobId); !ok {
		t.Fatal("RUN_FAILED has a data source now: a failed run must appear in run.failed_age")
	}
}

func TestProcessTask_ExhaustedRetriesAreReachableAsRunRetryExhausted(t *testing.T) {
	useTestPolicy(t, artifactPolicy())
	task := newRunnableTask(t, "fail_retryable", "n", `{}`)
	database.DB.Model(&models.Task{}).Where("job_id = ?", task.JobId).UpdateColumn("retry_index", maxRetries)

	ProcessTask(context.Background(), task.JobId, "alert-src-worker")

	if _, ok := sampleFor(t, "run.retries_exhausted_age", task.JobId); !ok {
		t.Fatal("RUN_RETRY_EXHAUSTED has a data source now: a run that hit the retry cap must appear in run.retries_exhausted_age")
	}
}

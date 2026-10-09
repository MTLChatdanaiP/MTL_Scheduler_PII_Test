package worker

// Batch 5 (5B): monitoring that tells the truth. Uses the package's existing TestMain for the database; every helper is
// prefixed b5 so this file cannot collide with another test file.

import (
	"context"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

type b5Run struct {
	jobID    string
	workerID string
}

// b5RunningRun creates a Running run claimed by a worker whose own heartbeat is workerHeartbeatAge old.
func b5RunningRun(t *testing.T, workerHeartbeatAge time.Duration) b5Run {
	t.Helper()
	r := b5Run{jobID: "b5-job-" + ulid.Make().String(), workerID: "b5-worker-" + ulid.Make().String()}
	now := time.Now().UTC()

	database.DB.Create(&models.Task{JobId: r.jobID, ExecutionChainId: r.jobID, TaskName: "b5", TaskType: "Dummy", Status: "Running"})
	database.DB.Create(&models.Attempt{AttemptId: ulid.Make().String(), JobId: r.jobID, WorkerId: r.workerID, AttemptNumber: 1, Status: "Started"})
	database.DB.Create(&models.WorkerHeartbeat{WorkerId: r.workerID, InstanceId: "b5-inst-" + r.workerID, OccurredAt: now.Add(-workerHeartbeatAge), RunningAttempts: 1, Capacity: 1})

	t.Cleanup(func() {
		database.DB.Unscoped().Where("job_id = ?", r.jobID).Delete(&models.Task{})
		database.DB.Unscoped().Where("job_id = ?", r.jobID).Delete(&models.Attempt{})
		database.DB.Unscoped().Where("job_id = ?", r.jobID).Delete(&models.EventEnvelope{})
		database.DB.Unscoped().Where("subject_id = ?", r.jobID).Delete(&models.MonitoringAnnotation{})
		database.DB.Unscoped().Where("worker_id = ?", r.workerID).Delete(&models.WorkerHeartbeat{})
	})
	return r
}

func b5Event(jobID, eventType string, age time.Duration) {
	at := time.Now().UTC().Add(-age)
	database.DB.Create(&models.EventEnvelope{JobId: jobID, EventID: ulid.Make().String(), EventType: eventType, OccurredAt: at, IngestedAt: at, Producer: "test", SchemaVersion: "1"})
}

func b5Annotations(jobID, annotationType string) []models.MonitoringAnnotation {
	var rows []models.MonitoringAnnotation
	database.DB.Where("subject_id = ? AND type = ?", jobID, annotationType).Find(&rows)
	return rows
}

// ---------------------------------------------------------------------------------------------------------- M1

// the headline: the handler is hung, the worker process is alive and keeps writing attempt.heartbeat. RUN_STUCK used to be
// impossible here because those heartbeats counted as "the run is alive".
func TestStuck_AHungHandlerIsFlaggedEvenThoughItsHeartbeatsKeepArriving(t *testing.T) {
	run := b5RunningRun(t, 5*time.Second) // the worker is alive
	b5Event(run.jobID, "task.started", 120*time.Second)
	b5Event(run.jobID, "attempt.heartbeat", 5*time.Second) // ...and keeps heartbeating

	if err := checkStuckTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(b5Annotations(run.jobID, "RUN_STUCK")) != 1 {
		t.Fatal("a run with no progress for 120 s on a live worker must be RUN_STUCK, however many heartbeats arrive")
	}
}

func TestStuck_ARunThatKeepsReportingProgressIsNotFlagged(t *testing.T) {
	run := b5RunningRun(t, 5*time.Second)
	b5Event(run.jobID, "task.started", 120*time.Second)
	b5Event(run.jobID, "task.progress", 5*time.Second)

	checkStuckTasks(context.Background())
	checkLostTasks(context.Background())
	if len(b5Annotations(run.jobID, "RUN_STUCK")) != 0 || len(b5Annotations(run.jobID, "RUN_LOST")) != 0 {
		t.Fatal("recent progress means the run is alive")
	}
}

func TestLost_ADeadWorkerIsStillFlagged(t *testing.T) {
	run := b5RunningRun(t, 10*time.Minute) // no heartbeat for 10 minutes
	b5Event(run.jobID, "task.started", 120*time.Second)
	b5Event(run.jobID, "attempt.heartbeat", 100*time.Second) // old heartbeat events from before it died

	if err := checkLostTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(b5Annotations(run.jobID, "RUN_LOST")) != 1 {
		t.Fatal("silent run + dead worker must be RUN_LOST")
	}
}

func TestResolve_AnAnnotationClearsOnProgressButNotOnHeartbeats(t *testing.T) {
	run := b5RunningRun(t, 5*time.Second)
	b5Event(run.jobID, "task.started", 300*time.Second)
	database.DB.Create(&models.MonitoringAnnotation{AnnotationID: ulid.Make().String(), Type: "RUN_STUCK", SubjectType: "TASK", SubjectID: run.jobID, DerivedAt: time.Now().UTC(), Evidence: "{}"})

	b5Event(run.jobID, "attempt.heartbeat", 2*time.Second)
	resolveClearedAnnotations(context.Background(), "RUN_STUCK")
	if a := b5Annotations(run.jobID, "RUN_STUCK"); len(a) != 1 || a[0].ResolvedAt != nil {
		t.Fatal("a heartbeat is not progress: the annotation must stay open")
	}

	b5Event(run.jobID, "task.progress", 2*time.Second)
	resolveClearedAnnotations(context.Background(), "RUN_STUCK")
	if a := b5Annotations(run.jobID, "RUN_STUCK"); len(a) != 1 || a[0].ResolvedAt == nil {
		t.Fatal("real progress must resolve the annotation")
	}
}

func TestRunSilentAfter_DefaultAndOverride(t *testing.T) {
	t.Setenv("RUN_SILENT_AFTER_SECONDS", "")
	if runSilentAfter() != stuckThreshold {
		t.Fatalf("default must be %v, got %v", stuckThreshold, runSilentAfter())
	}
	t.Setenv("RUN_SILENT_AFTER_SECONDS", "300")
	if runSilentAfter() != 300*time.Second {
		t.Fatalf("override ignored: %v", runSilentAfter())
	}
	for _, bad := range []string{"abc", "0", "-5", "1.5"} {
		t.Setenv("RUN_SILENT_AFTER_SECONDS", bad)
		if runSilentAfter() != stuckThreshold {
			t.Errorf("%q must fall back to the default, got %v", bad, runSilentAfter())
		}
	}
}

func TestStuck_TheThresholdIsReadWhenUsed(t *testing.T) {
	run := b5RunningRun(t, 5*time.Second)
	b5Event(run.jobID, "task.started", 120*time.Second)
	t.Setenv("RUN_SILENT_AFTER_SECONDS", "600") // 120 s of silence is fine under a 10 minute limit

	checkStuckTasks(context.Background())
	if len(b5Annotations(run.jobID, "RUN_STUCK")) != 0 {
		t.Fatal("the configured limit must be honoured")
	}
}

// ---------------------------------------------------------------------------------------------------------- M3

func TestNewestHeartbeat_OneRowPerWorkerAndOnePerInstance(t *testing.T) {
	prefix := "b5hb-" + ulid.Make().String() + "-"
	now := time.Now().UTC()
	for w := 0; w < 3; w++ {
		for i := 0; i < 50; i++ {
			database.DB.Create(&models.WorkerHeartbeat{WorkerId: prefix + string(rune('a'+w)), InstanceId: prefix + "inst-" + string(rune('a'+w)), OccurredAt: now.Add(-time.Duration(i) * time.Minute)})
		}
	}
	t.Cleanup(func() { database.DB.Unscoped().Where("worker_id LIKE ?", prefix+"%").Delete(&models.WorkerHeartbeat{}) })

	for name, fetch := range map[string]func(context.Context) ([]models.WorkerHeartbeat, error){"worker": newestHeartbeatPerWorker, "instance": newestHeartbeatPerInstance} {
		rows, err := fetch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		mine := 0
		for _, r := range rows {
			if len(r.WorkerId) >= len(prefix) && r.WorkerId[:len(prefix)] == prefix {
				mine++
				if now.Sub(r.OccurredAt) > time.Second {
					t.Errorf("per %s: must be the NEWEST heartbeat, got one %v old", name, now.Sub(r.OccurredAt))
				}
			}
		}
		if mine != 3 {
			t.Errorf("per %s: 150 heartbeat rows for 3 workers must come back as 3 rows, got %d", name, mine)
		}
	}
}

func TestGetComponentInstances_ReportsTheNewestHeartbeatOfEachInstance(t *testing.T) {
	workerID := "b5ci-" + ulid.Make().String()
	instance := "inst-" + workerID
	now := time.Now().UTC()
	database.DB.Create(&models.Worker{WorkerId: workerID, InstanceId: instance, ComponentType: "Worker", StartedAt: now.Add(-time.Hour)})
	for i := 0; i < 20; i++ {
		database.DB.Create(&models.WorkerHeartbeat{WorkerId: workerID, InstanceId: instance, OccurredAt: now.Add(-time.Duration(i+3) * time.Minute)})
	}
	database.DB.Create(&models.WorkerHeartbeat{WorkerId: workerID, InstanceId: instance, OccurredAt: now.Add(-2 * time.Second)})
	t.Cleanup(func() {
		database.DB.Unscoped().Where("worker_id = ?", workerID).Delete(&models.Worker{})
		database.DB.Unscoped().Where("worker_id = ?", workerID).Delete(&models.WorkerHeartbeat{})
	})

	instances, err := GetComponentInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range instances {
		if i.InstanceID == instance {
			if age := now.Sub(i.LastSeenAt); age > 10*time.Second {
				t.Fatalf("LastSeenAt must be the newest heartbeat (2 s old), it is %v old", age)
			}
			return
		}
	}
	t.Fatal("instance missing")
}

// ---------------------------------------------------------------------------------------------------------- M4

func TestAdjustVerdictForIdle(t *testing.T) {
	cases := []struct {
		subsystem, verdict string
		busy               bool
		want               string
	}{
		{"event-ingestion", "degraded", false, "available"}, // idle: nothing new is not a fault
		{"projection", "degraded", false, "available"},
		{"event-ingestion", "degraded", true, "degraded"}, // work is in flight but nothing arrives: a real fault
		{"projection", "degraded", true, "degraded"},
		{"redis-queue-inspection", "degraded", false, "degraded"}, // the sampler runs on a timer: always strict
		{"event-ingestion", "unavailable", false, "unavailable"},  // no data at all is never softened
		{"event-ingestion", "available", false, "available"},
	}
	for _, c := range cases {
		if got := adjustVerdictForIdle(c.subsystem, c.verdict, c.busy); got != c.want {
			t.Errorf("%s %s busy=%v: got %s, want %s", c.subsystem, c.verdict, c.busy, got, c.want)
		}
	}
}

func TestWorkInFlight(t *testing.T) {
	tx := database.DB.Begin()
	defer tx.Rollback() // nothing below is ever committed
	tx.Unscoped().Where("1 = 1").Delete(&models.Task{})
	ctx := context.Background()
	now := time.Now().UTC()

	if workInFlightIn(ctx, tx) {
		t.Fatal("an empty system has nothing in flight")
	}
	tx.Create(&models.Task{JobId: "b5-idle-1", ExecutionChainId: "b5-idle-1", TaskName: "n", TaskType: "Dummy", Status: "Completed"})
	tx.Create(&models.Task{JobId: "b5-idle-2", ExecutionChainId: "b5-idle-2", TaskName: "n", TaskType: "Dummy", Status: "Pending", RunAt: now.Add(time.Hour)})
	if workInFlightIn(ctx, tx) {
		t.Fatal("finished runs and a Pending run that is not due yet are not work in flight")
	}
	tx.Create(&models.Task{JobId: "b5-due", ExecutionChainId: "b5-due", TaskName: "n", TaskType: "Dummy", Status: "Pending", RunAt: now.Add(-time.Minute)})
	if !workInFlightIn(ctx, tx) {
		t.Fatal("a due Pending run is work in flight")
	}
	tx.Unscoped().Where("job_id = ?", "b5-due").Delete(&models.Task{})
	tx.Create(&models.Task{JobId: "b5-running", ExecutionChainId: "b5-running", TaskName: "n", TaskType: "Dummy", Status: "Running"})
	if !workInFlightIn(ctx, tx) {
		t.Fatal("a Running run is work in flight")
	}
}

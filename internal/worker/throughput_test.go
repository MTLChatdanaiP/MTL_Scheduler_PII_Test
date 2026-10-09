package worker

// RFC-005 §7 Queue Projection "throughput estimates" and Component Health Projection "build revision".

import (
	"context"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

// The counts are global, so every assertion is a DELTA against what was already in the database.
func TestThroughputPerMinute_CountsOnlyFinishedRunsInsideTheWindow(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	before := throughputPerMinute(ctx, now)

	add := func(eventType string, ago time.Duration) {
		subject := "tp-" + ulid.Make().String()
		seedEvent(t, subject, eventType, now.Add(-ago))
	}
	add("task.completed", 30*time.Second) // counts
	add("task.failed", 2*time.Minute)     // counts
	add("task.completed", 4*time.Minute)  // counts
	add("task.completed", 6*time.Minute)  // outside the 5 minute window
	add("task.progress", 10*time.Second)  // not a finished run
	add("task.started", 10*time.Second)   // not a finished run

	got := throughputPerMinute(ctx, now) - before
	want := 3.0 / throughputWindow.Minutes()
	if got < want-0.0001 || got > want+0.0001 {
		t.Fatalf("three finished runs in five minutes is %.2f per minute, the estimate grew by %.4f", want, got)
	}
}

// the estimate is actually written onto the sample the dashboard reads
func TestTheRealQueueSamplerRecordsThroughput(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		seedEvent(t, "tp-"+ulid.Make().String(), "task.completed", now.Add(-10*time.Second))
	}

	sample, err := SampleQueueHealth(ctx, 100)
	if err != nil {
		t.Fatalf("the sampler could not read the queue: %v", err)
	}
	// at least the ten finished runs this test just recorded, spread over the five minute window
	if sample.ThroughputPerMinute < 10.0/throughputWindow.Minutes() {
		t.Fatalf("the queue sample must carry the throughput estimate, got %.3f", sample.ThroughputPerMinute)
	}
}

func TestCreateComponent_RecordsTheBuildRevisionItWasStartedFrom(t *testing.T) {
	t.Setenv("BUILD_REVISION", "worker-rev-9")
	workerID := "bld-" + ulid.Make().String()

	w := CreateComponent(context.Background(), workerID, "Worker")
	t.Cleanup(func() {
		database.DB.Unscoped().Where("worker_id = ?", workerID).Delete(&models.Worker{})
		DeleteWorkerCounter(workerID)
	})

	var stored models.Worker
	database.DB.Where("instance_id = ?", w.InstanceId).First(&stored)
	if stored.BuildRevision != "worker-rev-9" {
		t.Fatalf("the worker must record the build it runs, got %q", stored.BuildRevision)
	}
}

func TestComponentInstances_CarryTheBuildRevision(t *testing.T) {
	workerID := "bld-" + ulid.Make().String()
	database.DB.Create(&models.Worker{WorkerId: workerID, InstanceId: "inst-" + workerID, ComponentType: "Worker", StartedAt: time.Now().UTC(), BuildRevision: "rev-77"})
	t.Cleanup(func() { database.DB.Unscoped().Where("worker_id = ?", workerID).Delete(&models.Worker{}) })

	instances, err := GetComponentInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range instances {
		if i.DisplayName == workerID {
			if i.BuildRevision != "rev-77" {
				t.Fatalf("revision = %q", i.BuildRevision)
			}
			return
		}
	}
	t.Fatal("the instance is missing")
}

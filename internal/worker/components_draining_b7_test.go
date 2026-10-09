package worker

// Batch 7 (RFC-010 §9): from the shutdown signal until the stop is recorded, a component is draining.

import (
	"context"
	"strings"
	"testing"
	"time"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func TestMarkDraining_IsRecordedAndReadsHealthyWithADrainingReason(t *testing.T) {
	w := b6Component(t, "Worker")
	// shutdown begins: the heartbeat stopped long ago relative to the thresholds
	database.DB.Create(&models.WorkerHeartbeat{WorkerId: w.WorkerId, InstanceId: w.InstanceId, OccurredAt: time.Now().UTC().Add(-10 * time.Minute)})

	MarkDraining(w.InstanceId)

	inst := b6Instance(t, w.InstanceId)
	if !inst.Draining(time.Now().UTC()) {
		t.Fatal("MarkDraining must make the instance read as draining")
	}
	verdict, reason := models.InstanceHealth(inst, time.Now().UTC(), workerDegradedAfter, workerOfflineAfter)
	if verdict != "HEALTHY" || !strings.Contains(reason, "draining") {
		t.Fatalf("got %s / %q", verdict, reason)
	}
}

func TestMarkDrainingOnShutdown_RecordsAtTheMomentTheContextIsCancelled(t *testing.T) {
	w := b6Component(t, "Reclaimer")
	ctx, cancel := context.WithCancel(context.Background())
	markDrainingOnShutdown(ctx, w.InstanceId)

	if b6Instance(t, w.InstanceId).Draining(time.Now().UTC()) {
		t.Fatal("must not be draining before the shutdown signal")
	}
	cancel()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b6Instance(t, w.InstanceId).Draining(time.Now().UTC()) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("draining was not recorded after the context was cancelled")
}

func TestComponentTransitionEvent_DrainingIsItsOwnNormalEvent(t *testing.T) {
	if got := componentTransitionEvent(models.ComponentInstance{}, "DRAINING"); got != "component.draining" {
		t.Fatalf("got %q, want component.draining", got)
	}
}

func TestComponentSweep_ADrainingInstanceEmitsComponentDrainingNotDegraded(t *testing.T) {
	w := b6Component(t, "Worker")
	database.DB.Create(&models.WorkerHeartbeat{WorkerId: w.WorkerId, InstanceId: w.InstanceId, OccurredAt: time.Now().UTC().Add(-2 * time.Second)})

	if err := checkComponentInstanceTransitions(context.Background()); err != nil { // first sighting: no event
		t.Fatal(err)
	}
	MarkDraining(w.InstanceId)
	if err := checkComponentInstanceTransitions(context.Background()); err != nil {
		t.Fatal(err)
	}

	var draining, other int64
	database.DB.Model(&models.EventEnvelope{}).Where("job_id = ? AND event_type = ?", w.InstanceId, "component.draining").Count(&draining)
	database.DB.Model(&models.EventEnvelope{}).Where("job_id = ? AND event_type IN ?", w.InstanceId, []string{"component.degraded", "component.offline"}).Count(&other)
	if draining != 1 || other != 0 {
		t.Fatalf("component.draining=%d degraded/offline=%d, want 1 and 0", draining, other)
	}
}

func TestWorkerStatusSweep_ADrainingWorkerBecomesDrainingNotDegraded(t *testing.T) {
	w := b6Component(t, "Worker")
	database.DB.Create(&models.WorkerHeartbeat{WorkerId: w.WorkerId, InstanceId: w.InstanceId, OccurredAt: time.Now().UTC().Add(-2 * time.Second)})

	if err := checkWorkerStatusTransitions(context.Background()); err != nil {
		t.Fatal(err)
	}
	MarkDraining(w.InstanceId)
	if err := checkWorkerStatusTransitions(context.Background()); err != nil {
		t.Fatal(err)
	}

	var draining int64
	database.DB.Model(&models.EventEnvelope{}).Where("job_id = ? AND event_type = ?", w.WorkerId, "worker.draining").Count(&draining)
	if draining != 1 {
		t.Fatalf("worker.draining=%d, want 1", draining)
	}
}

func TestDrainingInstances_ListsOnlyRecentUnstoppedDrainers(t *testing.T) {
	running := b6Component(t, "Worker")
	draining := b6Component(t, "Worker")
	finished := b6Component(t, "Worker")
	longAgo := b6Component(t, "Worker")

	MarkDraining(draining.InstanceId)
	MarkDraining(finished.InstanceId)
	MarkStopped(finished.InstanceId)
	database.DB.Model(&models.Worker{}).Where("instance_id = ?", longAgo.InstanceId).Update("draining_at", time.Now().UTC().Add(-models.DrainWindow-time.Minute))

	set := drainingInstances(context.Background())
	if !set[draining.InstanceId] || set[running.InstanceId] || set[finished.InstanceId] || set[longAgo.InstanceId] {
		t.Fatalf("got %v", set)
	}
}

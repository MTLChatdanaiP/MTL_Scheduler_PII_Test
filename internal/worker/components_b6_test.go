package worker

// Batch 6 (RFC-010 §9): a deliberate shutdown is "stopped", not "offline". Uses the package's existing TestMain database.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func b6Component(t *testing.T, componentType string) models.Worker {
	t.Helper()
	w := CreateComponent(context.Background(), "b6-"+ulid.Make().String(), componentType)
	t.Cleanup(func() {
		database.DB.Unscoped().Where("worker_id = ?", w.WorkerId).Delete(&models.Worker{})
		database.DB.Unscoped().Where("worker_id = ?", w.WorkerId).Delete(&models.WorkerHeartbeat{})
		database.DB.Unscoped().Where("job_id IN ?", []string{w.WorkerId, w.InstanceId}).Delete(&models.EventEnvelope{})
	})
	return w
}

func b6Instance(t *testing.T, instanceID string) models.ComponentInstance {
	t.Helper()
	all, err := GetComponentInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range all {
		if i.InstanceID == instanceID {
			return i
		}
	}
	t.Fatalf("instance %s not enumerated", instanceID)
	return models.ComponentInstance{}
}

func TestMarkStopped_IsRecordedAndTheInstanceReadsOfflineWithAGracefulReason(t *testing.T) {
	w := b6Component(t, "Worker")
	database.DB.Create(&models.WorkerHeartbeat{WorkerId: w.WorkerId, InstanceId: w.InstanceId, OccurredAt: time.Now().UTC().Add(-3 * time.Second)})

	before := b6Instance(t, w.InstanceId)
	if before.GracefullyStopped() {
		t.Fatal("a running instance must not read as stopped")
	}

	MarkStopped(w.InstanceId)

	after := b6Instance(t, w.InstanceId)
	if !after.GracefullyStopped() {
		t.Fatal("MarkStopped must make the instance read as gracefully stopped")
	}
	verdict, reason := models.InstanceHealth(after, time.Now().UTC(), workerDegradedAfter, workerOfflineAfter)
	if verdict != "OFFLINE" || !strings.Contains(reason, "stopped gracefully") {
		t.Fatalf("got %s / %q", verdict, reason)
	}
}

func TestMarkStopped_WritesEvenWhenTheComponentContextIsAlreadyCancelled(t *testing.T) {
	// shutdown begins by cancelling the context the component ran under. MarkStopped must not use it.
	w := b6Component(t, "Scheduler")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = ctx

	MarkStopped(w.InstanceId)

	if !b6Instance(t, w.InstanceId).GracefullyStopped() {
		t.Fatal("the stop was not recorded")
	}
}

func TestComponentTransitionEvent_StoppedIsNotCriticalButACrashIs(t *testing.T) {
	now := time.Now().UTC()
	stopped := models.ComponentInstance{LastSeenAt: now.Add(-5 * time.Second), StoppedAt: now}
	if got := componentTransitionEvent(stopped, "OFFLINE"); got != "component.stopped" {
		t.Errorf("a deliberate stop emits %q, want component.stopped", got)
	}
	crashed := models.ComponentInstance{LastSeenAt: now.Add(-10 * time.Minute)}
	if got := componentTransitionEvent(crashed, "OFFLINE"); got != "component.offline" {
		t.Errorf("a crash emits %q, want component.offline", got)
	}
	if got := componentTransitionEvent(crashed, "DEGRADED"); got != "component.degraded" {
		t.Errorf("got %q", got)
	}
}

func TestWorkerStatusSweep_AStoppedWorkerBecomesStoppedNotOffline(t *testing.T) {
	w := b6Component(t, "Worker")
	database.DB.Create(&models.WorkerHeartbeat{WorkerId: w.WorkerId, InstanceId: w.InstanceId, OccurredAt: time.Now().UTC().Add(-2 * time.Second)})

	if err := checkWorkerStatusTransitions(context.Background()); err != nil { // first sighting: records "online", no event
		t.Fatal(err)
	}
	MarkStopped(w.InstanceId)
	if err := checkWorkerStatusTransitions(context.Background()); err != nil {
		t.Fatal(err)
	}

	var stoppedEvents, offlineEvents int64
	database.DB.Model(&models.EventEnvelope{}).Where("job_id = ? AND event_type = ?", w.WorkerId, "worker.stopped").Count(&stoppedEvents)
	database.DB.Model(&models.EventEnvelope{}).Where("job_id = ? AND event_type = ?", w.WorkerId, "worker.offline").Count(&offlineEvents)
	if stoppedEvents != 1 || offlineEvents != 0 {
		t.Fatalf("worker.stopped=%d worker.offline=%d, want 1 and 0", stoppedEvents, offlineEvents)
	}
}

func TestComponentSweep_AStoppedInstanceEmitsComponentStopped(t *testing.T) {
	w := b6Component(t, "Reclaimer")
	database.DB.Create(&models.WorkerHeartbeat{WorkerId: w.WorkerId, InstanceId: w.InstanceId, OccurredAt: time.Now().UTC().Add(-2 * time.Second)})

	if err := checkComponentInstanceTransitions(context.Background()); err != nil {
		t.Fatal(err)
	}
	MarkStopped(w.InstanceId)
	if err := checkComponentInstanceTransitions(context.Background()); err != nil {
		t.Fatal(err)
	}

	var stopped, offline int64
	database.DB.Model(&models.EventEnvelope{}).Where("job_id = ? AND event_type = ?", w.InstanceId, "component.stopped").Count(&stopped)
	database.DB.Model(&models.EventEnvelope{}).Where("job_id = ? AND event_type = ?", w.InstanceId, "component.offline").Count(&offline)
	if stopped != 1 || offline != 0 {
		t.Fatalf("component.stopped=%d component.offline=%d, want 1 and 0", stopped, offline)
	}
}

func TestStoppedInstances_ListsOnlyInstancesThatRecordedAStop(t *testing.T) {
	running := b6Component(t, "Worker")
	gone := b6Component(t, "Worker")
	MarkStopped(gone.InstanceId)

	set := stoppedInstances(context.Background())
	if !set[gone.InstanceId] || set[running.InstanceId] {
		t.Fatalf("stopped set wrong: gone=%v running=%v", set[gone.InstanceId], set[running.InstanceId])
	}
}

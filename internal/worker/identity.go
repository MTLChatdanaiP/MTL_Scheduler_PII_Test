package worker

import (
	"context"
	"fmt"
	"os"
	"time"

	"MTL_Scheduler_PII_Test/internal/buildinfo"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"

	"github.com/oklog/ulid/v2"
)

// RFC-004 §5 Worker Registration
func CreateWorker(ctx context.Context, workerId string) models.Worker {
	return CreateComponent(ctx, workerId, "Worker")
}

// MarkStopped records that the component instance shut down on purpose (RFC-010 §9). Components call it as their run loop returns.
//
// It uses its OWN short-lived context: the one the component was running under is, by definition, already cancelled when shutdown
// begins, and a write on a cancelled context is skipped (the same trap shutdown.Run documents).
func MarkStopped(instanceID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := database.DB.WithContext(ctx).Model(&models.Worker{}).Where("instance_id = ?", instanceID).Update("stopped_at", time.Now().UTC()).Error; err != nil {
		fmt.Println("failed to record graceful stop for", instanceID, ":", err)
	}
}

// MarkDraining records that the component saw the shutdown signal and has stopped claiming new work (RFC-010 §9). It may still be
// finishing a task, so it reads as healthy-and-draining rather than as a missing heartbeat. Like MarkStopped it uses its own context.
func MarkDraining(instanceID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := database.DB.WithContext(ctx).Model(&models.Worker{}).Where("instance_id = ?", instanceID).Update("draining_at", time.Now().UTC()).Error; err != nil {
		fmt.Println("failed to record draining for", instanceID, ":", err)
	}
}

// markDrainingOnShutdown starts a goroutine that records draining the moment ctx is cancelled (the shutdown signal), wherever the
// component's loop happens to be at that instant. It ends with ctx, or when the component is done and cancels its own watcher.
func markDrainingOnShutdown(ctx context.Context, instanceID string) {
	go func() {
		<-ctx.Done()
		MarkDraining(instanceID)
	}()
}

func CreateComponent(ctx context.Context, workerId string, componentType string) models.Worker {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "idk insert error messages?"
	}

	worker := models.Worker{WorkerId: workerId, InstanceId: ulid.Make().String(), ComponentType: componentType, Hostname: hostname, StartedAt: time.Now().UTC(), BuildRevision: buildinfo.Revision()}

	database.DB.WithContext(ctx).Create(&worker)
	RegisterWorkerCounter(workerId)
	return worker
}

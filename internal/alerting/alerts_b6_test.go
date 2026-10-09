package alerts

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

// RFC-010 §9: restarting a worker on purpose must not raise a heartbeat-age alert for the instance that stopped.
func TestWorkerHeartbeatAge_SkipsAnInstanceThatStoppedOnPurpose(t *testing.T) {
	// This package has no TestMain (its other tests need no database), so connect here -- and skip, rather than abort the package,
	// where no database is configured.
	if os.Getenv("DB_HOST") == "" {
		t.Skip("no database configured")
	}
	if database.DB == nil {
		database.ConnectDatabase()
	}
	database.DB.AutoMigrate(&models.Worker{}, &models.WorkerHeartbeat{})

	id := "b6-alert-" + ulid.Make().String()
	instStopped, instCrashed := id+"-stopped", id+"-crashed"
	stoppedWorker, crashedWorker := id+"-ws", id+"-wc"
	old := time.Now().UTC().Add(-10 * time.Minute)
	stoppedAt := old.Add(time.Second)

	database.DB.Create(&models.Worker{WorkerId: stoppedWorker, InstanceId: instStopped, ComponentType: "Worker", StartedAt: old, StoppedAt: &stoppedAt})
	database.DB.Create(&models.Worker{WorkerId: crashedWorker, InstanceId: instCrashed, ComponentType: "Worker", StartedAt: old})
	database.DB.Create(&models.WorkerHeartbeat{WorkerId: stoppedWorker, InstanceId: instStopped, OccurredAt: old})
	database.DB.Create(&models.WorkerHeartbeat{WorkerId: crashedWorker, InstanceId: instCrashed, OccurredAt: old})
	t.Cleanup(func() {
		database.DB.Unscoped().Where("worker_id IN ?", []string{stoppedWorker, crashedWorker}).Delete(&models.Worker{})
		database.DB.Unscoped().Where("worker_id IN ?", []string{stoppedWorker, crashedWorker}).Delete(&models.WorkerHeartbeat{})
	})

	samples, err := resolveWorkerHeartbeatAge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var sawStopped, sawCrashed bool
	for _, s := range samples {
		sawStopped = sawStopped || s.SubjectID == stoppedWorker
		sawCrashed = sawCrashed || s.SubjectID == crashedWorker
	}
	if sawStopped {
		t.Error("a deliberately stopped worker must not produce a heartbeat-age sample (it would page someone for a deploy)")
	}
	if !sawCrashed {
		t.Error("a worker that went silent WITHOUT stopping must still be sampled")
	}
}

// Batch 7 (RFC-010 §9): a draining instance has stopped heartbeating on purpose, for a bounded time. It raises no heartbeat-age alert
// while it drains; once the drain window has passed it is judged by the ordinary rules again.
func TestWorkerHeartbeatAge_SkipsADrainingInstanceOnlyWhileTheDrainWindowLasts(t *testing.T) {
	if os.Getenv("DB_HOST") == "" {
		t.Skip("no database configured")
	}
	if database.DB == nil {
		database.ConnectDatabase()
	}
	database.DB.AutoMigrate(&models.Worker{}, &models.WorkerHeartbeat{})

	id := "b7-alert-" + ulid.Make().String()
	instDraining, instExpired := id+"-draining", id+"-expired"
	workerDraining, workerExpired := id+"-wd", id+"-we"
	old := time.Now().UTC().Add(-10 * time.Minute)
	now := time.Now().UTC()
	longAgo := now.Add(-models.DrainWindow - time.Minute)

	database.DB.Create(&models.Worker{WorkerId: workerDraining, InstanceId: instDraining, StartedAt: old, DrainingAt: &now})
	database.DB.Create(&models.Worker{WorkerId: workerExpired, InstanceId: instExpired, StartedAt: old, DrainingAt: &longAgo})
	database.DB.Create(&models.WorkerHeartbeat{WorkerId: workerDraining, InstanceId: instDraining, OccurredAt: old})
	database.DB.Create(&models.WorkerHeartbeat{WorkerId: workerExpired, InstanceId: instExpired, OccurredAt: old})
	t.Cleanup(func() {
		database.DB.Unscoped().Where("worker_id IN ?", []string{workerDraining, workerExpired}).Delete(&models.Worker{})
		database.DB.Unscoped().Where("worker_id IN ?", []string{workerDraining, workerExpired}).Delete(&models.WorkerHeartbeat{})
	})

	samples, err := resolveWorkerHeartbeatAge(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var sawDraining, sawExpired bool
	for _, s := range samples {
		sawDraining = sawDraining || s.SubjectID == workerDraining
		sawExpired = sawExpired || s.SubjectID == workerExpired
	}
	if sawDraining {
		t.Error("a draining instance must not raise a heartbeat-age sample")
	}
	if !sawExpired {
		t.Error("an instance whose drain window has passed must be judged by its heartbeat age again")
	}
}

package worker

import (
	"context"
	"os"
	"time"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"

	"github.com/oklog/ulid/v2"
)

// RFC-004 §5 Worker Registration
func CreateWorker(ctx context.Context, workerId string) models.Worker {
	return CreateComponent(ctx, workerId, "Worker")
}

func CreateComponent(ctx context.Context, workerId string, componentType string) models.Worker {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "idk insert error messages?"
	}

	worker := models.Worker{WorkerId: workerId, InstanceId: ulid.Make().String(), ComponentType: componentType, Hostname: hostname, StartedAt: time.Now().UTC()}

	database.DB.WithContext(ctx).Create(&worker)
	RegisterWorkerCounter(workerId)
	return worker
}

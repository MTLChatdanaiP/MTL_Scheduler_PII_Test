package worker

import (
	"context"
	"os"
	"time"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"

	"github.com/oklog/ulid/v2"
)

// RFC-004 §5 Worker Registration: "On startup, a worker announces: identity, version, supported job types,
// queues, concurrency, start time." Subset implemented: identity, hostname, start time
func CreateWorker(ctx context.Context, workerId string) models.Worker {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "idk insert error messages?"
	}

	worker := models.Worker{WorkerId: workerId, InstanceId: ulid.Make().String(), Hostname: hostname, StartedAt: time.Now()}

	database.DB.WithContext(ctx).Create(&worker)
	RegisterWorkerCounter(workerId)
	return worker
}

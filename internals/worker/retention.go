package worker

import (
	"context"
	"fmt"
	"time"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
)

var retentionWindow = getEnvIntOrDefault("PII_RETENTION_HOURS", 24) //int
var sweepIntervalHours = getEnvIntOrDefault("RETENTION_SWEEP_INTERVAL_HOURS", 1)

func StartRetentionSweep(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		cutoff := time.Now().Add(-time.Duration(retentionWindow) * time.Hour)

		if err := database.DB.WithContext(ctx).
			Where("sampled_at < ?", cutoff).
			Delete(&models.QueueHealth{}).Error; err != nil {
			fmt.Println("Failed to prune old queue health samples:", err)
		}

		if err := database.DB.WithContext(ctx).
			Where("occurred_at < ?", cutoff).
			Delete(&models.WorkerHeartbeat{}).Error; err != nil {
			fmt.Println("Failed to prune old worker heartbeat samples:", err)
		}

		if err := database.DB.WithContext(ctx).
			Where("resolved_at IS NOT NULL AND resolved_at < ?", cutoff).
			Delete(&models.MonitoringAnnotation{}).Error; err != nil {
			fmt.Println("Failed to prune old annotation samples:", err)
		}

		time.Sleep(time.Duration(sweepIntervalHours) * time.Hour) // retention doesn't need to run often
	}
}

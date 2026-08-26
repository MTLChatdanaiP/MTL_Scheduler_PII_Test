package worker

import (
	"context"
	"fmt"
	"time"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
)

const retentionWindow = 24 * time.Hour // TODO: pick a real value — short for testing, longer for "realistic"

func StartRetentionSweep(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		cutoff := time.Now().Add(-retentionWindow)

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

		time.Sleep(1 * time.Hour) // retention doesn't need to run often
	}
}

package testutil

import (
	"context"

	"MTL_Scheduler_PII_Test/internals/cache"
	"MTL_Scheduler_PII_Test/internals/database"
)

func ResetAll(ctx context.Context) {
	tables := []string{
		"tasks",
		"pii_records",
		"pii_vaults",
		"policy_activations",
		"alerts",
		"event_envelopes",
		"run_projections",
		"workers",
		"worker_heartbeats",
		"attempts",
		"queue_healths",
		"execution_chains",
		"schedule_definitions",
		"monitoring_annotations",
		"monitoring_healths",
	}

	for _, table := range tables {
		if err := database.DB.Exec("TRUNCATE TABLE " + table + " RESTART IDENTITY CASCADE").Error; err != nil {
			panic("testutil.ResetAll: failed to truncate " + table + ": " + err.Error())
		}
	}

	if err := cache.Client.FlushAll(ctx).Err(); err != nil {
		panic("testutil.ResetAll: failed to flush Redis: " + err.Error())
	}
}

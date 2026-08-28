package integration_test

import (
	"context"
	"testing"

	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/events"
	"MTL_Scheduler_PII_Test/internals/models"
)

func TestLogEvent_CarriesLineageFields(t *testing.T) {
	ctx := context.Background()

	task := models.Task{
		JobId:            ulid.Make().String(),
		TaskName:         "lineage_test",
		TaskType:         "dummy",
		Status:           "Pending",
		ExecutionChainId: "test-chain-" + ulid.Make().String(),
		ParentRunId:      "test-parent-" + ulid.Make().String(),
		RetryIndex:       2,
	}
	database.DB.Create(&task)

	t.Cleanup(func() {
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.Task{})
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.EventEnvelope{})
	})

	events.LogEvent(ctx, task.JobId, "task.started", "worker")

	var envelope models.EventEnvelope
	database.DB.Where("job_id = ? AND event_type = ?", task.JobId, "task.started").First(&envelope)

	if envelope.ExecutionChainID != task.ExecutionChainId {
		t.Errorf("expected ExecutionChainID %q, got %q", task.ExecutionChainId, envelope.ExecutionChainID)
	}
	if envelope.ParentRunID != task.ParentRunId {
		t.Errorf("expected ParentRunID %q, got %q", task.ParentRunId, envelope.ParentRunID)
	}
	if envelope.RetryIndex != task.RetryIndex {
		t.Errorf("expected RetryIndex %d, got %d", task.RetryIndex, envelope.RetryIndex)
	}
}

func TestUpdateProjection_ReflectsFinalState(t *testing.T) {
	ctx := context.Background()

	task := models.Task{
		JobId:    ulid.Make().String(),
		TaskName: "projection_final_state_test",
		TaskType: "dummy",
		Status:   "Pending",
	}
	database.DB.Create(&task)

	t.Cleanup(func() {
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.Task{})
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.EventEnvelope{})
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.RunProjection{})
	})

	events.LogEvent(ctx, task.JobId, "task.created", "api")
	events.LogEvent(ctx, task.JobId, "task.queued", "scheduler")
	events.LogEvent(ctx, task.JobId, "task.started", "worker")
	events.LogEvent(ctx, task.JobId, "task.completed", "worker")

	var projection models.RunProjection
	database.DB.Where("job_id = ?", task.JobId).First(&projection)

	if projection.CurrentStatus != "Completed" {
		t.Errorf("expected CurrentStatus Completed, got %q", projection.CurrentStatus)
	}
}

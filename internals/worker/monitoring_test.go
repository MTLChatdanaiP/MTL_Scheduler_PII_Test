package worker

import (
	"context"
	"testing"
	"time"

	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"

	"github.com/oklog/ulid/v2"
)

func TestCheckStuckTasks_CreatesAnnotationOnce(t *testing.T) {
	ctx := context.Background()

	task := models.Task{
		JobId:    ulid.Make().String(),
		TaskName: "stuck_test",
		TaskType: "dummy",
		Status:   "Running",
	}
	database.DB.Create(&task)

	attempt := models.Attempt{
		AttemptId: ulid.Make().String(),
		JobId:     task.JobId,
		WorkerId:  "test-worker-stuck",
		Status:    "Started",
		StartedAt: time.Now().Add(-2 * time.Minute),
	}
	database.DB.Create(&attempt)

	heartbeat := models.WorkerHeartbeat{
		WorkerId:   "test-worker-stuck",
		InstanceId: ulid.Make().String(),
		OccurredAt: time.Now(), // fresh — worker itself is healthy
	}
	database.DB.Create(&heartbeat)

	staleEvent := models.EventEnvelope{
		JobId:      task.JobId,
		EventID:    ulid.Make().String(),
		EventType:  "task.started",
		OccurredAt: time.Now().Add(-90 * time.Second), // older than stuckThreshold
		Producer:   "worker",
	}
	database.DB.Create(&staleEvent)

	t.Cleanup(func() {
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.Task{})
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.Attempt{})
		database.DB.Where("worker_id = ?", "test-worker-stuck").Delete(&models.WorkerHeartbeat{})
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.EventEnvelope{})
		database.DB.Where("subject_id = ?", task.JobId).Delete(&models.MonitoringAnnotation{})
	})

	checkStuckTasks(ctx)

	var annotations []models.MonitoringAnnotation
	database.DB.Where("subject_id = ?", task.JobId).Find(&annotations)

	if len(annotations) != 1 {
		t.Errorf("expected 1 annotation created from checkStuckTasks(), got %d", len(annotations))
	}

	checkStuckTasks(ctx)

	database.DB.Where("subject_id = ?", task.JobId).Find(&annotations)

	if len(annotations) != 1 {
		t.Errorf("expected 1 annotation created from checkStuckTasks(), got %d", len(annotations))
	}
}

func TestResolveClearedAnnotations_ResolvesOnCompletion(t *testing.T) {
	ctx := context.Background()

	task := models.Task{
		JobId:    ulid.Make().String(),
		TaskName: "resolve_test",
		TaskType: "dummy",
		Status:   "Completed", // already finished by the time resolution runs
	}
	database.DB.Create(&task)

	finalEvent := models.EventEnvelope{
		JobId:      task.JobId,
		EventID:    ulid.Make().String(),
		EventType:  "task.completed",
		OccurredAt: time.Now(),
		Producer:   "worker",
	}
	database.DB.Create(&finalEvent)

	annotation := models.MonitoringAnnotation{
		AnnotationID: ulid.Make().String(),
		Type:         "RUN_STUCK",
		SubjectType:  "TASK",
		SubjectID:    task.JobId,
		DerivedAt:    time.Now().Add(-5 * time.Minute),
	}
	database.DB.Create(&annotation)

	t.Cleanup(func() {
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.Task{})
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.EventEnvelope{})
		database.DB.Where("subject_id = ?", task.JobId).Delete(&models.MonitoringAnnotation{})
	})

	task.Status = "Completed"
	database.DB.Save(&task)

	resolveClearedAnnotations(ctx, "RUN_STUCK")

	var updated models.MonitoringAnnotation
	database.DB.Where("subject_id = ?", task.JobId).First(&updated)

	if updated.ResolvedAt != nil {
		t.Error("expected ResolvedAt to be set, was still zero")
	}
}

func TestCheckLostTasks_NoHeartbeatRowTriggersLost(t *testing.T) {
	ctx := context.Background()

	task := models.Task{
		JobId:    ulid.Make().String(),
		TaskName: "lost_test",
		TaskType: "dummy",
		Status:   "Running",
	}
	database.DB.Create(&task)

	attempt := models.Attempt{
		AttemptId: ulid.Make().String(),
		JobId:     task.JobId,
		WorkerId:  "test-worker-lost", // deliberately no heartbeat row created for this worker
		Status:    "Started",
		StartedAt: time.Now().Add(-2 * time.Minute),
	}
	database.DB.Create(&attempt)

	staleEvent := models.EventEnvelope{
		JobId:      task.JobId,
		EventID:    ulid.Make().String(),
		EventType:  "task.started",
		OccurredAt: time.Now().Add(-90 * time.Second),
		Producer:   "worker",
	}
	database.DB.Create(&staleEvent)

	t.Cleanup(func() {
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.Task{})
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.Attempt{})
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.EventEnvelope{})
		database.DB.Where("subject_id = ?", task.JobId).Delete(&models.MonitoringAnnotation{})
	})

	checkLostTasks(ctx)

	var annotations []models.MonitoringAnnotation
	database.DB.Where("subject_id = ?", task.JobId).Find(&annotations)

	if len(annotations) != 1 {
		t.Fatalf("expected 1 annotation, got %d", len(annotations))
	}
	if annotations[0].Type != "RUN_LOST" {
		t.Errorf("expected Type RUN_LOST, got %q", annotations[0].Type)
	}

}

func TestCheckDuplicateExecution_DetectsTwoActiveAttempts(t *testing.T) {
	ctx := context.Background()

	task := models.Task{
		JobId:    ulid.Make().String(),
		TaskName: "duplicate_test",
		TaskType: "dummy",
		Status:   "Running",
	}
	database.DB.Create(&task)

	attempt1 := models.Attempt{
		AttemptId: ulid.Make().String(),
		JobId:     task.JobId,
		WorkerId:  "test-worker-a",
		Status:    "Started",
	}
	database.DB.Create(&attempt1)

	attempt2 := models.Attempt{
		AttemptId: ulid.Make().String(),
		JobId:     task.JobId,
		WorkerId:  "test-worker-b",
		Status:    "Started",
	}
	database.DB.Create(&attempt2)

	t.Cleanup(func() {
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.Task{})
		database.DB.Where("job_id = ?", task.JobId).Delete(&models.Attempt{})
		database.DB.Where("subject_id = ?", task.JobId).Delete(&models.MonitoringAnnotation{})
	})

	checkDuplicateExecution(ctx)

	var annotations []models.MonitoringAnnotation
	database.DB.Where("subject_id = ?", task.JobId).Find(&annotations)

	if len(annotations) != 1 {
		t.Fatalf("expected 1 annotation, got %d", len(annotations))
	}
	if annotations[0].Type != "RUN_DUPLICATE_SUSPECTED" {
		t.Errorf("expected Type RUN_DUPLICATE_SUSPECTED, got %q", annotations[0].Type)
	}
}

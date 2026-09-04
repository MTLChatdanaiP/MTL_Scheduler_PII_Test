package worker

import (
	"context"
	"testing"
	"time"

	"MTL_Scheduler_PII_Test/internals/cache"
	redisdb "MTL_Scheduler_PII_Test/internals/cache"
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
	"os"

	"github.com/joho/godotenv"
)

func TestMain(m *testing.M) {
	godotenv.Load("../../.env")
	database.ConnectDatabase()
	redisdb.ConnectRedis()

	database.DB.AutoMigrate(
		&models.Task{}, &models.PIIRecord{}, &models.EventEnvelope{}, &models.RunProjection{},
		&models.Worker{}, &models.WorkerHeartbeat{}, &models.QueueHealth{}, &models.Attempt{},
		&models.ExecutionChain{}, &models.ScheduleDefinition{}, &models.MonitoringAnnotation{}, &models.MonitoringHealth{},
	)

	redisdb.Client.XGroupCreateMkStream(context.Background(), cache.TaskStream, WorkerGroupA, "$")

	os.Exit(m.Run())
}
func TestRunHandler(t *testing.T) {
	tests := []struct {
		name     string
		taskType string
		want     ExecutionOutcome
	}{
		{"retryable failure returns immediately", "fail_retryable", RetryableFailure},
		{"permanent failure returns immediately", "fail_permanent", NonRetryableFailure},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := models.Task{TaskType: tt.taskType}

			start := time.Now()
			got, _ := runHandler(context.Background(), task)
			elapsed := time.Since(start)

			if got != tt.want {
				t.Errorf("runHandler() = %v, want %v", got, tt.want)
			}
			if elapsed > time.Second {
				t.Errorf("expected immediate return, took %v", elapsed)
			}
		})
	}
}

func TestRunHandler_SuccessPath(t *testing.T) {
	original := progressChunkDuration
	progressChunkDuration = 10 * time.Millisecond       // fast for testing
	defer func() { progressChunkDuration = original }() // restore it after the test

	tests := []struct {
		name     string
		taskType string
		want     ExecutionOutcome
	}{
		{"allowed/default task type, process for 30s", "Dummy", Success},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := models.Task{TaskType: tt.taskType}

			start := time.Now()
			got, _ := runHandler(context.Background(), task)
			elapsed := time.Since(start)

			if got != tt.want {
				t.Errorf("runHandler() = %v, want %v", got, tt.want)
			}
			expectedMinDuration := progressChunkDuration * time.Duration(progressChunkCount)
			expectedMaxDuration := expectedMinDuration + (2 * time.Second)

			if elapsed < expectedMinDuration || elapsed > expectedMaxDuration {
				t.Errorf("expected at least %v of processing, only took %v", expectedMinDuration, elapsed)
			}
		})
	}
}

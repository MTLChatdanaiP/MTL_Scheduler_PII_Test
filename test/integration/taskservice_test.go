package integration_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/joho/godotenv"
	"github.com/oklog/ulid/v2"
	"gorm.io/gorm/clause"

	alerts "MTL_Scheduler_PII_Test/internals/alerting"
	"MTL_Scheduler_PII_Test/internals/cache"
	redisdb "MTL_Scheduler_PII_Test/internals/cache"
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
	"MTL_Scheduler_PII_Test/internals/pii"
	"MTL_Scheduler_PII_Test/internals/taskservice"
	"MTL_Scheduler_PII_Test/internals/worker"
)

func TestMain(m *testing.M) {
	godotenv.Load("../../.env")
	database.ConnectDatabase()
	redisdb.ConnectRedis()

	database.DB.AutoMigrate(
		&models.Task{}, &models.PIIRecord{}, &models.EventEnvelope{}, &models.RunProjection{},
		&models.Worker{}, &models.WorkerHeartbeat{}, &models.QueueHealth{}, &models.Attempt{},
		&models.ExecutionChain{}, &models.ScheduleDefinition{}, &models.MonitoringAnnotation{}, &models.MonitoringHealth{},
		&models.PIIVault{},
		&models.PolicyActivation{}, &models.Alert{}, &models.Notification{},
	)

	// Same reason as internals/worker: CreateTask_Direct dereferences the
	// LoadedPolicy atomic pointer, which is nil until something activates a policy.
	if _, err := pii.ActivatePolicy(context.Background(), "../../policies/default.json", "STARTUP", "system"); err != nil {
		panic("integration tests: failed to activate PII policy: " + err.Error())
	}
	if _, err := alerts.ActivateRules(context.Background(), "../../internals/alerting/rules.json", "system"); err != nil {
		panic("integration tests: failed to activate alert rules: " + err.Error())
	}
	redisdb.Client.XGroupCreateMkStream(context.Background(), cache.TaskStream, worker.WorkerGroupA, "$")

	os.Exit(m.Run())
}

func TestCreateTaskRecord_RedactsAndStoresFindings(t *testing.T) {
	ctx := context.Background()

	task := models.Task{
		TaskName: "integration_test",
		TaskType: "dummy",
		Payload:  "contact me at test@example.com",
	}

	result := taskservice.CreateTask_Direct(ctx, task)

	if strings.Contains(result.Payload, "test@example.com") {
		t.Error("payload still contains raw email, expected redaction")
	}

	var records []models.PIIRecord
	database.DB.Where("job_id = ?", result.JobId).Find(&records)

	if len(records) != 1 {
		t.Fatalf("expected 1 PIIRecord, got %d", len(records))
	}

	expectedFingerprint := pii.Fingerprint("test@example.com")
	if records[0].FingerprintValue != expectedFingerprint {
		t.Errorf("expected fingerprint %q, got %q", expectedFingerprint, records[0].FingerprintValue)
	}

	var vaultEntries []models.PIIVault
	database.DB.Where("job_id = ?", result.JobId).Find(&vaultEntries)

	if len(vaultEntries) != 1 {
		t.Fatalf("expected 1 PIIVault entry, got %d", len(vaultEntries))
	}

	decrypted, err := pii.Decrypt(vaultEntries[0].EncryptedValue)
	if err != nil {
		t.Fatalf("failed to decrypt vault entry: %v", err)
	}
	if decrypted != "test@example.com" {
		t.Errorf("expected decrypted vault value %q, got %q", "test@example.com", decrypted)
	}
}

func TestCreateTaskRecord_DoesNotDuplicateExecutionChain(t *testing.T) {
	ctx := context.Background()

	parent := models.Task{
		TaskName: "chain_dedup_test",
		TaskType: "dummy",
		Payload:  "no pii here",
	}
	parentResult := taskservice.CreateTask_Direct(ctx, parent)

	t.Cleanup(func() {
		database.DB.Where("job_id = ?", parentResult.JobId).Delete(&models.Task{})
		database.DB.Where("execution_chain_id = ?", parentResult.ExecutionChainId).Delete(&models.ExecutionChain{})
	})

	child := models.Task{
		JobId:            ulid.Make().String(),
		TaskName:         parent.TaskName,
		TaskType:         "dummy",
		Status:           "Pending",
		ExecutionChainId: parentResult.ExecutionChainId, // reuse the SAME chain
		ParentRunId:      parentResult.JobId,
		RetryIndex:       1,
	}
	database.DB.Create(&child)

	t.Cleanup(func() {
		database.DB.Where("job_id = ?", child.JobId).Delete(&models.Task{})
	})

	duplicateChain := models.ExecutionChain{ExecutionChainId: parentResult.ExecutionChainId}
	database.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "execution_chain_id"}},
		DoNothing: true,
	}).Create(&duplicateChain)

	var count int64
	database.DB.Model(&models.ExecutionChain{}).
		Where("execution_chain_id = ?", parentResult.ExecutionChainId).
		Count(&count)

	if count != 1 {
		t.Errorf("expected 1 ExecutionChain row, got %d", count)
	}
}

func TestProcessTask_TerminalTaskIgnored(t *testing.T) {
	ctx := context.Background()

	task := models.Task{
		TaskName: "completed_test",
		TaskType: "dummy",
		Payload:  "no pii here",
		Status:   "Completed",
	}
	completed_task := taskservice.CreateTask_Direct(ctx, task)

	t.Cleanup(func() {
		database.DB.Where("job_id = ?", completed_task.JobId).Delete(&models.Task{})
		database.DB.Where("job_id = ?", completed_task.JobId).Delete(&models.Attempt{})
	})

	worker.ProcessTask(ctx, completed_task.JobId, "nonexistent-worker")

	var attempts []models.Attempt
	database.DB.Where("job_id = ?", completed_task.JobId).Find(&attempts)

	if len(attempts) != 0 {
		t.Errorf("expected 0 attempts, got %d", len(attempts))
	}
}

func TestProcessTask_RetryableFailureCreatesChildRun(t *testing.T) {

	ctx := context.Background()

	test_task := models.Task{
		TaskName: "retryable_task_test",
		TaskType: "fail_retryable",
		Payload:  "no pii here",
	}
	retryable_task := taskservice.CreateTask_Direct(ctx, test_task)

	t.Cleanup(func() {
		database.DB.Where("job_id = ?", retryable_task.JobId).Delete(&models.Task{})
	})

	worker.ProcessTask(ctx, retryable_task.JobId, "nonexistent-worker")

	var tasks []models.Task
	database.DB.Where("parent_run_id = ?", retryable_task.JobId).Find(&tasks)

	if len(tasks) != 1 {
		t.Errorf("expected 1 child, got %d", len(tasks))
	}
	if tasks[0].RetryIndex != 1 {
		t.Errorf("expected retry_index of 1, got %d", tasks[0].RetryIndex)
	}
	if tasks[0].ExecutionChainId != retryable_task.ExecutionChainId {
		t.Errorf("expected ExecutionChainId %s, got %s", retryable_task.ExecutionChainId, tasks[0].ExecutionChainId)
	}
}

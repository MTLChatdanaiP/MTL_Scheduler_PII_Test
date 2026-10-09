package worker

// PRD §56 / RFC-006 §35: retention. Every destructive call here is scoped to rows the test created itself (a unique
// job-id or name prefix), so running these against a database that holds real data cannot touch that data.

import (
	"context"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

var longAgoRetention = time.Now().UTC().Add(-100 * time.Hour)

func physical(t *testing.T, model interface{}, where string, args ...interface{}) int64 {
	t.Helper()
	var n int64
	database.DB.Unscoped().Model(model).Where(where, args...).Count(&n)
	return n
}

// ---------------------------------------------------------------------------
// The settings
// ---------------------------------------------------------------------------

func TestRetentionKnobs_AreOffUnlessSetAndReadWhenUsed(t *testing.T) {
	knobs := map[string]func() int{
		"PII_VAULT_RETENTION_HOURS":     vaultRetentionHours,
		"PII_FINDINGS_RETENTION_HOURS":  findingsRetentionHours,
		"ARTIFACT_BODY_RETENTION_HOURS": artifactBodyRetentionHours,
	}
	for name, get := range knobs {
		for _, v := range []string{"", "abc", "0", "-5", "1.5"} {
			t.Setenv(name, v)
			if got := get(); got != 0 {
				t.Errorf("%s=%q gave %d, want 0 (keep forever): nothing may be deleted unless asked for", name, v, got)
			}
		}
		t.Setenv(name, "12")
		if got := get(); got != 12 {
			t.Errorf("%s=12 set after startup gave %d", name, got)
		}
	}
}

func TestPIIRetentionHours_DoesNotControlTheVault(t *testing.T) {
	// PII_RETENTION_HOURS is about monitoring samples. It must never start deleting raw values, whatever it says.
	t.Setenv("PII_RETENTION_HOURS", "1")
	t.Setenv("PII_VAULT_RETENTION_HOURS", "")
	if vaultRetentionHours() != 0 {
		t.Fatal("an existing variable became destructive")
	}
}

func TestTableName_MatchesWhatGormUses(t *testing.T) {
	want := map[string]interface{}{"pii_vaults": &models.PIIVault{}, "pii_records": &models.PIIRecord{}, "execution_artifacts": &models.ExecutionArtifact{}}
	for table, model := range want {
		if got := tableName(model); got != table {
			t.Errorf("tableName = %q, want %q", got, table)
		}
	}
}

// ---------------------------------------------------------------------------
// Physical deletion, which is what "retention" never actually did
// ---------------------------------------------------------------------------

func seedVault(t *testing.T, prefix string, n int, createdAt time.Time) {
	t.Helper()
	rows := make([]models.PIIVault, n)
	for i := range rows {
		rows[i] = models.PIIVault{JobId: prefix + "-" + ulid.Make().String(), Type: "Email", Index: 1, EncryptedValue: "x"}
	}
	if err := database.DB.CreateInBatches(&rows, 500).Error; err != nil {
		t.Fatal(err)
	}
	database.DB.Model(&models.PIIVault{}).Where("job_id LIKE ?", prefix+"-%").Where("created_at > ?", createdAt.Add(time.Hour)).UpdateColumn("created_at", createdAt)
	t.Cleanup(func() { database.DB.Unscoped().Where("job_id LIKE ?", prefix+"-%").Delete(&models.PIIVault{}) })
}

func TestDeleteOlderBatched_RemovesOldRowsPhysicallyAndKeepsNewOnes(t *testing.T) {
	prefix := "ret-" + ulid.Make().String()
	seedVault(t, prefix+"-old", 5, longAgoRetention)
	seedVault(t, prefix+"-new", 5, time.Now().UTC())

	n, err := deleteOlderBatched(context.Background(), "pii_vaults", "created_at", time.Now().UTC().Add(-24*time.Hour), "job_id LIKE ?", prefix+"%")
	if err != nil || n != 5 {
		t.Fatalf("expected 5 deleted, got %d (%v)", n, err)
	}
	if got := physical(t, &models.PIIVault{}, "job_id LIKE ?", prefix+"-old-%"); got != 0 {
		t.Fatalf("%d old rows are STILL PHYSICALLY in the table: that is the soft-delete bug", got)
	}
	if got := physical(t, &models.PIIVault{}, "job_id LIKE ?", prefix+"-new-%"); got != 5 {
		t.Fatalf("recent rows must stay, found %d of 5", got)
	}
}

func TestDeleteOlderBatched_AlsoRemovesRowsThatWereOnlySoftDeleted(t *testing.T) {
	prefix := "ret-" + ulid.Make().String()
	seedVault(t, prefix+"-old", 3, longAgoRetention)
	database.DB.Where("job_id LIKE ?", prefix+"-old-%").Delete(&models.PIIVault{}) // what the old sweep did: hide, not remove

	if got := physical(t, &models.PIIVault{}, "job_id LIKE ?", prefix+"-old-%"); got != 3 {
		t.Fatalf("setup: the soft-deleted rows should still be physically present, found %d", got)
	}

	deleteOlderBatched(context.Background(), "pii_vaults", "created_at", time.Now().UTC().Add(-24*time.Hour), "job_id LIKE ?", prefix+"%")

	if got := physical(t, &models.PIIVault{}, "job_id LIKE ?", prefix+"-old-%"); got != 0 {
		t.Fatalf("rows hidden by an earlier soft delete must be physically removed too, %d remain", got)
	}
}

func TestDeleteOlderBatched_ClearsMoreThanOneBatchAndNeverTouchesOtherScopes(t *testing.T) {
	mine := "ret-" + ulid.Make().String()
	other := "ret-" + ulid.Make().String()
	seedVault(t, mine+"-old", pruneBatchSize*2+500, longAgoRetention) // 2500 rows: three batches
	seedVault(t, other+"-old", 5, longAgoRetention)

	n, err := deleteOlderBatched(context.Background(), "pii_vaults", "created_at", time.Now().UTC().Add(-24*time.Hour), "job_id LIKE ?", mine+"%")
	if err != nil || n != int64(pruneBatchSize*2+500) {
		t.Fatalf("expected %d deleted across batches, got %d (%v)", pruneBatchSize*2+500, n, err)
	}
	if got := physical(t, &models.PIIVault{}, "job_id LIKE ?", other+"-old-%"); got != 5 {
		t.Fatalf("a different scope was touched: %d of 5 remain", got)
	}
}

func TestExpireArtifactBodies_BlanksTheTextAndKeepsTheRecord(t *testing.T) {
	prefix := "ret-" + ulid.Make().String()
	mk := func(job string, producedAt time.Time) {
		a := models.ExecutionArtifact{JobID: job, AttemptID: "a", Source: "JOB_RESULT", SanitizedBody: "some sanitized text", ScanStatus: "DETECTED", FindingCount: 2, ProducedAt: producedAt, OriginalBytes: 99}
		database.DB.Create(&a)
	}
	oldJob, newJob := prefix+"-old", prefix+"-new"
	mk(oldJob, longAgoRetention)
	mk(newJob, time.Now().UTC())
	t.Cleanup(func() { database.DB.Unscoped().Where("job_id LIKE ?", prefix+"%").Delete(&models.ExecutionArtifact{}) })

	n, err := expireArtifactBodies(context.Background(), time.Now().UTC().Add(-24*time.Hour), "job_id LIKE ?", prefix+"%")
	if err != nil || n != 1 {
		t.Fatalf("expected 1 body expired, got %d (%v)", n, err)
	}

	var old, fresh models.ExecutionArtifact
	database.DB.Where("job_id = ?", oldJob).First(&old)
	database.DB.Where("job_id = ?", newJob).First(&fresh)

	if old.SanitizedBody != "" || !old.BodyExpired {
		t.Fatalf("the old body must be blanked and flagged: %+v", old)
	}
	if old.ScanStatus != "DETECTED" || old.FindingCount != 2 || old.OriginalBytes != 99 {
		t.Fatalf("the record must keep its facts: %+v", old)
	}
	if fresh.SanitizedBody == "" || fresh.BodyExpired {
		t.Fatal("a recent artifact must be untouched")
	}
	if n2, _ := expireArtifactBodies(context.Background(), time.Now().UTC().Add(-24*time.Hour), "job_id LIKE ?", prefix+"%"); n2 != 0 {
		t.Fatalf("an already-expired body is not expired twice, got %d", n2)
	}
}

// ---------------------------------------------------------------------------
// Each setting touches only its own table
// ---------------------------------------------------------------------------

func seedAllPII(t *testing.T, prefix string) {
	t.Helper()
	job := prefix + "-" + ulid.Make().String()
	database.DB.Create(&models.PIIVault{JobId: job, Type: "Email", Index: 1, EncryptedValue: "x"})
	database.DB.Create(&models.PIIRecord{JobID: job, Type: "Email", Source: "JOB_PAYLOAD", FingerprintValue: "fp"})
	database.DB.Create(&models.ExecutionArtifact{JobID: job, AttemptID: "a", Source: "JOB_RESULT", SanitizedBody: "text", ProducedAt: longAgoRetention})
	database.DB.Create(&models.AuditRecord{Actor: "tester", Action: "PII_RAW_VALUE_READ", SubjectType: "JOB", SubjectID: job, OccurredAt: longAgoRetention})
	for _, tbl := range []string{"pii_vaults", "pii_records"} {
		database.DB.Exec("UPDATE "+tbl+" SET created_at = ? WHERE job_id LIKE ?", longAgoRetention, prefix+"-%")
	}
	database.DB.Exec("UPDATE audit_records SET created_at = ? WHERE subject_id LIKE ?", longAgoRetention, prefix+"-%")
	t.Cleanup(func() {
		database.DB.Unscoped().Where("job_id LIKE ?", prefix+"-%").Delete(&models.PIIVault{})
		database.DB.Unscoped().Where("job_id LIKE ?", prefix+"-%").Delete(&models.PIIRecord{})
		database.DB.Unscoped().Where("job_id LIKE ?", prefix+"-%").Delete(&models.ExecutionArtifact{})
		database.DB.Unscoped().Where("subject_id LIKE ?", prefix+"-%").Delete(&models.AuditRecord{})
	})
}

func counts(t *testing.T, prefix string) (vault, findings, bodies, audit int64) {
	t.Helper()
	vault = physical(t, &models.PIIVault{}, "job_id LIKE ?", prefix+"-%")
	findings = physical(t, &models.PIIRecord{}, "job_id LIKE ?", prefix+"-%")
	bodies = physical(t, &models.ExecutionArtifact{}, "job_id LIKE ? AND sanitized_body <> ''", prefix+"-%")
	audit = physical(t, &models.AuditRecord{}, "subject_id LIKE ?", prefix+"-%")
	return
}

func TestPrune_NothingIsDeletedUnlessASettingIsOn(t *testing.T) {
	prefix := "ret-" + ulid.Make().String()
	seedAllPII(t, prefix)
	for _, n := range []string{"PII_VAULT_RETENTION_HOURS", "PII_FINDINGS_RETENTION_HOURS", "ARTIFACT_BODY_RETENTION_HOURS"} {
		t.Setenv(n, "")
	}

	pruneExpiredPIIWith(context.Background(), prefix)

	if v, f, b, a := counts(t, prefix); v != 1 || f != 1 || b != 1 || a != 1 {
		t.Fatalf("with every setting off nothing may change: vault=%d findings=%d bodies=%d audit=%d", v, f, b, a)
	}
}

func TestPrune_EachSettingTouchesOnlyItsOwnTableAndAuditRecordsAreNeverDeleted(t *testing.T) {
	ctx := context.Background()
	for _, n := range []string{"PII_VAULT_RETENTION_HOURS", "PII_FINDINGS_RETENTION_HOURS", "ARTIFACT_BODY_RETENTION_HOURS"} {
		t.Setenv(n, "")
	}
	prefix := "ret-" + ulid.Make().String()
	seedAllPII(t, prefix)

	t.Setenv("PII_VAULT_RETENTION_HOURS", "24")
	pruneExpiredPIIWith(ctx, prefix)
	if v, f, b, a := counts(t, prefix); v != 0 || f != 1 || b != 1 || a != 1 {
		t.Fatalf("only the vault setting is on, so only the vault goes: vault=%d findings=%d bodies=%d audit=%d", v, f, b, a)
	}

	t.Setenv("PII_FINDINGS_RETENTION_HOURS", "24")
	pruneExpiredPIIWith(ctx, prefix)
	if v, f, b, a := counts(t, prefix); v != 0 || f != 0 || b != 1 || a != 1 {
		t.Fatalf("now findings too, but not artifacts: vault=%d findings=%d bodies=%d audit=%d", v, f, b, a)
	}

	t.Setenv("ARTIFACT_BODY_RETENTION_HOURS", "24")
	pruneExpiredPIIWith(ctx, prefix)
	if v, f, b, a := counts(t, prefix); v != 0 || f != 0 || b != 0 || a != 1 {
		t.Fatalf("bodies blanked, and the audit record is NEVER deleted automatically: vault=%d findings=%d bodies=%d audit=%d", v, f, b, a)
	}
	if physical(t, &models.ExecutionArtifact{}, "job_id LIKE ?", prefix+"-%") != 1 {
		t.Fatal("blanking a body must keep the artifact row")
	}
}

// ---------------------------------------------------------------------------
// The existing monitoring-sample sweep, which never removed a row
// ---------------------------------------------------------------------------

func TestPruneMonitoringSamples_RemovesThemPhysically(t *testing.T) {
	prefix := "ret-" + ulid.Make().String()
	ctx := context.Background()
	old, recent := longAgoRetention, time.Now().UTC()

	database.DB.Create(&models.QueueHealth{QueueName: prefix + "-q-old", SampledAt: old})
	database.DB.Create(&models.QueueHealth{QueueName: prefix + "-q-new", SampledAt: recent})
	database.DB.Create(&models.WorkerHeartbeat{WorkerId: prefix + "-w-old", OccurredAt: old})
	database.DB.Create(&models.WorkerHeartbeat{WorkerId: prefix + "-w-new", OccurredAt: recent})
	resolvedOld, resolvedNew := old, recent
	database.DB.Create(&models.MonitoringAnnotation{AnnotationID: ulid.Make().String(), Type: "RUN_STUCK", SubjectType: "TASK", SubjectID: prefix + "-a-resolved-old", ResolvedAt: &resolvedOld})
	database.DB.Create(&models.MonitoringAnnotation{AnnotationID: ulid.Make().String(), Type: "RUN_STUCK", SubjectType: "TASK", SubjectID: prefix + "-a-resolved-new", ResolvedAt: &resolvedNew})
	database.DB.Create(&models.MonitoringAnnotation{AnnotationID: ulid.Make().String(), Type: "RUN_STUCK", SubjectType: "TASK", SubjectID: prefix + "-a-open"})
	t.Cleanup(func() {
		database.DB.Unscoped().Where("queue_name LIKE ?", prefix+"%").Delete(&models.QueueHealth{})
		database.DB.Unscoped().Where("worker_id LIKE ?", prefix+"%").Delete(&models.WorkerHeartbeat{})
		database.DB.Unscoped().Where("subject_id LIKE ?", prefix+"%").Delete(&models.MonitoringAnnotation{})
	})

	pruneMonitoringSamples(ctx, time.Now().UTC().Add(-24*time.Hour), monitoringScope{queue: prefix + "%", worker: prefix + "%", subject: prefix + "%"})

	if physical(t, &models.QueueHealth{}, "queue_name = ?", prefix+"-q-old") != 0 || physical(t, &models.QueueHealth{}, "queue_name = ?", prefix+"-q-new") != 1 {
		t.Fatal("old queue samples must be physically gone and recent ones kept")
	}
	if physical(t, &models.WorkerHeartbeat{}, "worker_id = ?", prefix+"-w-old") != 0 || physical(t, &models.WorkerHeartbeat{}, "worker_id = ?", prefix+"-w-new") != 1 {
		t.Fatal("old heartbeats must be physically gone and recent ones kept")
	}
	if physical(t, &models.MonitoringAnnotation{}, "subject_id = ?", prefix+"-a-resolved-old") != 0 {
		t.Fatal("an old RESOLVED annotation must be physically gone")
	}
	if physical(t, &models.MonitoringAnnotation{}, "subject_id = ?", prefix+"-a-resolved-new") != 1 || physical(t, &models.MonitoringAnnotation{}, "subject_id = ?", prefix+"-a-open") != 1 {
		t.Fatal("a recent resolved annotation and any OPEN annotation must be kept")
	}
}

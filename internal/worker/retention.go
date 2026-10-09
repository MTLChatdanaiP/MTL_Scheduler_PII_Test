package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/events"
	"MTL_Scheduler_PII_Test/internal/models"
)

// Retention (PRD §56 "retain only information required to explain a finding", RFC-006 §35 open question #10).
//
// TWO things were wrong before:
//
//  1. The sweep never removed a row. Every model it touches embeds gorm.Model, so Delete() is a SOFT delete: it
//     sets deleted_at and keeps the row. The data was hidden from queries and still physically in the table.
//     Everything here removes rows with a real DELETE.
//  2. It never touched PII-bearing data. pii_vault holds every encrypted raw value, and nothing ever expired it.
//
// The PII settings are separate, opt-in, and OFF by default: deleting a raw value is irreversible, so an
// existing variable (PII_RETENTION_HOURS, which is about monitoring samples) must never start doing it.

const (
	pruneBatchSize         = 1000
	pruneMaxBatchesPerPass = 50
)

// hoursFromEnv takes a variable name and returns its value in hours, read now, where unset, invalid or below 1
// means 0, which means "keep forever".
func hoursFromEnv(name string) int {
	n := getEnvIntOrDefault(name, 0)
	if n < 1 {
		return 0
	}
	return n
}

// vaultRetentionHours: how long encrypted raw values are kept (PII_VAULT_RETENTION_HOURS). IRREVERSIBLE.
func vaultRetentionHours() int { return hoursFromEnv("PII_VAULT_RETENTION_HOURS") }

// findingsRetentionHours: how long findings (fingerprints and metadata) are kept (PII_FINDINGS_RETENTION_HOURS).
func findingsRetentionHours() int { return hoursFromEnv("PII_FINDINGS_RETENTION_HOURS") }

// artifactBodyRetentionHours: how long a stored artifact's TEXT is kept (ARTIFACT_BODY_RETENTION_HOURS).
func artifactBodyRetentionHours() int { return hoursFromEnv("ARTIFACT_BODY_RETENTION_HOURS") }

// tableName takes a model and returns its table name as GORM sees it, so the names are never typed by hand.
func tableName(model interface{}) string {
	stmt := &gorm.Statement{DB: database.DB}
	if err := stmt.Parse(model); err != nil {
		return ""
	}
	return stmt.Schema.Table
}

func cutoffHours(h int) time.Time {
	return time.Now().UTC().Add(-time.Duration(h) * time.Hour)
}

// deleteOlderBatched takes a table, its time column and a cutoff, and returns how many rows it physically deleted,
// removing rows older than the cutoff in batches of pruneBatchSize, at most pruneMaxBatchesPerPass batches, so a
// first run on a large table cannot hold one long lock. scopeWhere (with its args) narrows it further and is
// used by tests so they can only ever touch rows they created; production passes "".
func deleteOlderBatched(ctx context.Context, table string, timeColumn string, cutoff time.Time, scopeWhere string, scopeArgs ...interface{}) (int64, error) {
	if table == "" {
		return 0, fmt.Errorf("unknown table")
	}

	scope := ""
	if scopeWhere != "" {
		scope = " AND (" + scopeWhere + ")"
	}
	query := fmt.Sprintf("DELETE FROM %s WHERE id IN (SELECT id FROM %s WHERE %s < ?%s ORDER BY id LIMIT ?)", table, table, timeColumn, scope)

	var total int64
	for i := 0; i < pruneMaxBatchesPerPass; i++ {
		args := append(append([]interface{}{cutoff}, scopeArgs...), pruneBatchSize)
		res := database.DB.WithContext(ctx).Exec(query, args...)
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if res.RowsAffected < pruneBatchSize {
			break
		}
	}
	return total, nil
}

// expireArtifactBodies takes a cutoff and returns how many artifact bodies it blanked, by setting sanitized_body to
// ” and body_expired to true on rows produced before the cutoff, in batches. The row itself is kept.
func expireArtifactBodies(ctx context.Context, cutoff time.Time, scopeWhere string, scopeArgs ...interface{}) (int64, error) {
	table := tableName(&models.ExecutionArtifact{})
	scope := ""
	if scopeWhere != "" {
		scope = " AND (" + scopeWhere + ")"
	}
	query := fmt.Sprintf("UPDATE %s SET sanitized_body = '', body_expired = true WHERE id IN (SELECT id FROM %s WHERE body_expired = false AND produced_at < ?%s ORDER BY id LIMIT ?)", table, table, scope)

	var total int64
	for i := 0; i < pruneMaxBatchesPerPass; i++ {
		args := append(append([]interface{}{cutoff}, scopeArgs...), pruneBatchSize)
		res := database.DB.WithContext(ctx).Exec(query, args...)
		if res.Error != nil {
			return total, res.Error
		}
		total += res.RowsAffected
		if res.RowsAffected < pruneBatchSize {
			break
		}
	}
	return total, nil
}

// pruneExpiredPII is the production entry point: every PII-bearing table, no narrowing.
func pruneExpiredPII(ctx context.Context) { pruneExpiredPIIWith(ctx, "") }

// pruneExpiredPIIWith applies each retention setting that is ON to its own table and only that table. jobPrefix,
// when not empty, narrows every table to job ids starting with it (tests); production passes "".
//
// audit_records are never deleted automatically: they are the record of who read raw values.
func pruneExpiredPIIWith(ctx context.Context, jobPrefix string) {
	scope, args := "", []interface{}{}
	if jobPrefix != "" {
		scope, args = "job_id LIKE ?", []interface{}{jobPrefix + "%"}
	}

	changed := int64(0)

	if h := vaultRetentionHours(); h > 0 {
		n, err := deleteOlderBatched(ctx, tableName(&models.PIIVault{}), "created_at", cutoffHours(h), scope, args...)
		changed += n
		logPrune("vault entries deleted", n, err)
	}
	if h := findingsRetentionHours(); h > 0 {
		n, err := deleteOlderBatched(ctx, tableName(&models.PIIRecord{}), "created_at", cutoffHours(h), scope, args...)
		changed += n
		logPrune("findings deleted", n, err)
	}
	if h := artifactBodyRetentionHours(); h > 0 {
		n, err := expireArtifactBodies(ctx, cutoffHours(h), scope, args...)
		changed += n
		logPrune("artifact bodies expired", n, err)
	}

	if changed > 0 && jobPrefix == "" {
		events.LogEvent(ctx, "system", "retention.pruned", "retention")
	}
}

func logPrune(what string, n int64, err error) {
	if err != nil {
		slog.Error("retention pass failed", "what", what, "deleted_so_far", n, "error", err)
		return
	}
	if n > 0 {
		slog.Info("retention", "what", what, "count", n) // counts only, never values
	}
}

// monitoringScope narrows the monitoring-sample sweep for tests; the zero value (production) narrows nothing.
type monitoringScope struct{ queue, worker, subject string }

// pruneMonitoringSamples removes queue-health samples, worker heartbeats and RESOLVED annotations older than the
// cutoff, physically and in batches. (It used Delete(), which only hid them, so these tables grew forever.)
func pruneMonitoringSamples(ctx context.Context, cutoff time.Time, scope monitoringScope) {
	type job struct {
		model interface{}
		col   string
		where string
		arg   string
	}
	jobs := []job{
		{&models.QueueHealth{}, "sampled_at", "queue_name LIKE ?", scope.queue},
		{&models.WorkerHeartbeat{}, "occurred_at", "worker_id LIKE ?", scope.worker},
		// an unresolved annotation has resolved_at NULL, and NULL < cutoff is never true, so it is kept
		{&models.MonitoringAnnotation{}, "resolved_at", "subject_id LIKE ?", scope.subject},
	}

	for _, j := range jobs {
		where, args := "", []interface{}{}
		if j.arg != "" {
			where, args = j.where, []interface{}{j.arg}
		}
		n, err := deleteOlderBatched(ctx, tableName(j.model), j.col, cutoff, where, args...)
		if err != nil {
			fmt.Println("Failed to prune old monitoring samples:", err)
		} else if n > 0 {
			slog.Info("retention", "what", "monitoring samples deleted", "table", tableName(j.model), "count", n)
		}
	}
}

func StartRetentionSweep(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		pruneMonitoringSamples(ctx, cutoffHours(retentionWindowHours()), monitoringScope{})
		pruneExpiredPII(ctx)

		time.Sleep(retentionSweepEvery()) // retention doesn't need to run often
	}
}

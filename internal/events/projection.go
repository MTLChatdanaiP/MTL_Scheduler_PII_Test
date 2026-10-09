package events

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

// RFC-005 §7 Projections, §16 Ordering.
//
// A projection is DERIVED data: it can always be rebuilt by replaying the event log, which is what
// RebuildRunProjections does. UpdateProjection keeps its signature and is still called by LogEventWith for every
// event, but it now runs the pure reducer (ReduceRun) inside a locked transaction instead of upserting the event's
// status blindly.

// UpdateProjection takes a run id, an event type and the event's own time, and applies the event to that run's
// projection. Events whose subject is not a run create nothing (the projection used to get a row for "system", worker
// ids, queue names and alert ids).
func UpdateProjection(ctx context.Context, jobId string, eventType string, occurredAt time.Time) {
	if !isRunScoped(jobId, eventType) {
		return
	}

	err := database.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// make sure a row exists, then lock it so two writers for one run (the API and a worker) cannot lose each
		// other's update. The first event now populates the row; it used to insert an empty one.
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.RunProjection{JobId: jobId}).Error; err != nil {
			return err
		}

		var current models.RunProjection
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("job_id = ?", jobId).First(&current).Error; err != nil {
			return err
		}

		next := ReduceRun(current, RunEvent{Type: eventType, At: occurredAt})
		refreshSourceCounts(tx, &next, eventType)

		return tx.Save(&next).Error
	})

	if err != nil {
		fmt.Println("FAILED TO UPDATE PROJECTION: ", err)
	}
}

// refreshSourceCounts recomputes the counts an event may have changed, from the tables they come from. They are
// RECOMPUTED, never incremented: an incremented count drifts on a replayed or lost event, a recomputed one cannot.
func refreshSourceCounts(tx *gorm.DB, p *models.RunProjection, eventType string) {
	if strings.HasPrefix(eventType, "attempt.") || strings.HasPrefix(eventType, "task.") {
		refreshAttemptFields(tx, p)
	}
	// pii findings are written BEFORE the task row exists, so task.created is when they first become countable
	if strings.HasPrefix(eventType, "pii.") || eventType == "task.created" {
		refreshPIICount(tx, p)
	}
}

func refreshAttemptFields(tx *gorm.DB, p *models.RunProjection) {
	var count int64
	tx.Model(&models.Attempt{}).Where("job_id = ?", p.JobId).Count(&count)
	p.AttemptCount = int(count)

	var latest models.Attempt
	if err := tx.Where("job_id = ?", p.JobId).Order("attempt_number DESC").First(&latest).Error; err == nil {
		p.LatestAttemptID, p.LatestAttemptStatus, p.LatestWorkerID = latest.AttemptId, latest.Status, latest.WorkerId
	} else {
		p.LatestAttemptID, p.LatestAttemptStatus, p.LatestWorkerID = "", "", ""
	}
}

func refreshPIICount(tx *gorm.DB, p *models.RunProjection) {
	var count int64
	tx.Model(&models.PIIRecord{}).Where("job_id = ?", p.JobId).Count(&count)
	p.PIIFindingCount = int(count)
}

func refreshAnnotationAndAlertCounts(tx *gorm.DB, p *models.RunProjection) {
	var annotations, alerts int64
	tx.Model(&models.MonitoringAnnotation{}).Where("subject_id = ? AND resolved_at IS NULL", p.JobId).Count(&annotations)
	tx.Model(&models.Alert{}).Where("subject_id = ? AND status = ?", p.JobId, "OPEN").Count(&alerts)
	p.ActiveAnnotationCount, p.OpenAlertCount = int(annotations), int(alerts)
}

func fillAllCounts(tx *gorm.DB, p *models.RunProjection) {
	refreshAttemptFields(tx, p)
	refreshPIICount(tx, p)
	refreshAnnotationAndAlertCounts(tx, p)
}

func tableOf(db *gorm.DB, model interface{}) string {
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(model); err != nil {
		return ""
	}
	return stmt.Schema.Table
}

// RefreshRunProjectionCounts takes a context and brings every run's active_annotation_count and open_alert_count
// into line with the annotations and alerts tables, by recomputing them in four bounded statements. The monitoring
// sweep calls it once per pass, so the counts are at most one sweep old. It touches only runs that have, or had, an
// annotation or alert, and it is idempotent.
func RefreshRunProjectionCounts(ctx context.Context) {
	db := database.DB.WithContext(ctx)
	rp, ann, al := tableOf(db, &models.RunProjection{}), tableOf(db, &models.MonitoringAnnotation{}), tableOf(db, &models.Alert{})

	statements := []string{
		fmt.Sprintf(`UPDATE %s SET active_annotation_count = c.n FROM (SELECT subject_id, COUNT(*) AS n FROM %s WHERE resolved_at IS NULL AND deleted_at IS NULL GROUP BY subject_id) c WHERE %s.job_id = c.subject_id AND %s.active_annotation_count <> c.n`, rp, ann, rp, rp),
		fmt.Sprintf(`UPDATE %s SET active_annotation_count = 0 WHERE active_annotation_count > 0 AND NOT EXISTS (SELECT 1 FROM %s a WHERE a.subject_id = %s.job_id AND a.resolved_at IS NULL AND a.deleted_at IS NULL)`, rp, ann, rp),
		fmt.Sprintf(`UPDATE %s SET open_alert_count = c.n FROM (SELECT subject_id, COUNT(*) AS n FROM %s WHERE status = 'OPEN' AND deleted_at IS NULL GROUP BY subject_id) c WHERE %s.job_id = c.subject_id AND %s.open_alert_count <> c.n`, rp, al, rp, rp),
		fmt.Sprintf(`UPDATE %s SET open_alert_count = 0 WHERE open_alert_count > 0 AND NOT EXISTS (SELECT 1 FROM %s a WHERE a.subject_id = %s.job_id AND a.status = 'OPEN' AND a.deleted_at IS NULL)`, rp, al, rp),
	}
	for _, s := range statements {
		if err := db.Exec(s).Error; err != nil {
			fmt.Println("FAILED TO REFRESH PROJECTION COUNTS: ", err)
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Rebuild: a projection is derived, so history can be repaired by replaying events.
// ---------------------------------------------------------------------------

// computeRunProjection takes a task and returns the projection its events imply, by folding them, in EventLess order,
// through the SAME reducer the live writer uses, then recomputing every count from its source table. A task with no
// events at all falls back to the task's own status.
func computeRunProjection(db *gorm.DB, task models.Task) models.RunProjection {
	var evs []models.EventEnvelope
	db.Where("job_id = ?", task.JobId).Find(&evs)
	SortEvents(evs)

	p := models.RunProjection{JobId: task.JobId}
	for _, e := range evs {
		p = ReduceRun(p, RunEvent{Type: e.EventType, At: e.OccurredAt})
	}
	if p.CurrentStatus == "" {
		p.CurrentStatus = task.Status
	}
	fillAllCounts(db, &p)
	return p
}

func sameTime(a, b time.Time) bool {
	return a.Truncate(time.Microsecond).Equal(b.Truncate(time.Microsecond))
}

// projectionsEqual compares what matters in two projections. Times are compared to the microsecond because the
// database does not keep nanoseconds.
func projectionsEqual(a, b models.RunProjection) bool {
	return a.CurrentStatus == b.CurrentStatus &&
		sameTime(a.QueuedAt, b.QueuedAt) && sameTime(a.StartedAt, b.StartedAt) && sameTime(a.CompletedAt, b.CompletedAt) && sameTime(a.LastEventAt, b.LastEventAt) &&
		a.RecoveryStarted == b.RecoveryStarted && a.WasReclaimed == b.WasReclaimed &&
		a.PIIFindingCount == b.PIIFindingCount && a.AttemptCount == b.AttemptCount &&
		a.LatestAttemptID == b.LatestAttemptID && a.LatestAttemptStatus == b.LatestAttemptStatus && a.LatestWorkerID == b.LatestWorkerID &&
		a.ActiveAnnotationCount == b.ActiveAnnotationCount && a.OpenAlertCount == b.OpenAlertCount &&
		a.Contradicted == b.Contradicted && a.ContradictionNote == b.ContradictionNote
}

// RebuildReport says what a rebuild found (and, when it applied, fixed). It holds counts only.
type RebuildReport struct {
	Examined int   // tasks looked at
	Missing  int   // tasks with no projection row
	Wrong    int   // rows that disagreed with what their events imply
	Junk     int64 // rows that belong to no task (a worker id, "system", a queue name ...)
}

// RebuildRunProjections takes a context and whether to apply, and returns what it found. With apply false it only
// reports. With apply true it rewrites every wrong or missing projection from the event log and deletes the rows that
// belong to no task. It is idempotent and safe to run at any time.
func RebuildRunProjections(ctx context.Context, apply bool) (RebuildReport, error) {
	return rebuildRunProjections(ctx, apply, "")
}

// rebuildRunProjections is RebuildRunProjections narrowed to job ids starting with jobPrefix (tests); "" narrows nothing.
func rebuildRunProjections(ctx context.Context, apply bool, jobPrefix string) (RebuildReport, error) {
	db := database.DB.WithContext(ctx)
	var report RebuildReport

	var lastID uint
	for {
		var tasks []models.Task
		q := db.Where("id > ?", lastID).Order("id ASC").Limit(500)
		if jobPrefix != "" {
			q = q.Where("job_id LIKE ?", jobPrefix+"%")
		}
		if err := q.Find(&tasks).Error; err != nil {
			return report, err
		}
		if len(tasks) == 0 {
			break
		}

		for _, task := range tasks {
			lastID = task.ID
			report.Examined++

			want := computeRunProjection(db, task)

			var stored models.RunProjection
			err := db.Unscoped().Where("job_id = ?", task.JobId).First(&stored).Error
			switch {
			case err != nil:
				report.Missing++
				if apply {
					if err := db.Create(&want).Error; err != nil {
						return report, err
					}
				}
			case stored.DeletedAt.Valid || !projectionsEqual(stored, want):
				report.Wrong++
				if apply {
					want.ID, want.CreatedAt = stored.ID, stored.CreatedAt
					if err := db.Unscoped().Save(&want).Error; err != nil {
						return report, err
					}
				}
			}
		}
	}

	rp, tk := tableOf(db, &models.RunProjection{}), tableOf(db, &models.Task{})
	where := fmt.Sprintf("job_id NOT IN (SELECT job_id FROM %s)", tk)
	args := []interface{}{}
	if jobPrefix != "" {
		where += " AND job_id LIKE ?"
		args = append(args, jobPrefix+"%")
	}

	var junk int64
	db.Unscoped().Table(rp).Where(where, args...).Count(&junk)
	report.Junk = junk
	if apply && junk > 0 {
		if err := db.Exec("DELETE FROM "+rp+" WHERE "+where, args...).Error; err != nil {
			return report, err
		}
	}

	return report, nil
}

// RebuildRunProjection rebuilds ONE run's projection from its events and returns whether it had to change.
func RebuildRunProjection(ctx context.Context, jobId string) (bool, error) {
	report, err := rebuildRunProjections(ctx, true, jobId)
	return report.Missing+report.Wrong > 0, err
}

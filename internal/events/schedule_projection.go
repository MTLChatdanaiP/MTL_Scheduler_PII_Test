package events

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

// RFC-005 §7 Schedule Projection, RFC-002 §14.

const (
	OutcomeDue     = "DUE"
	OutcomeCreated = "CREATED"
	OutcomeSkipped = "SKIPPED"
)

// ScheduleEvent is the part of an event the occurrence ledger needs.
type ScheduleEvent struct {
	Type         string
	At           time.Time
	ScheduleID   string
	OccurrenceID string
	RunID        string
}

// expectedFromOccurrenceID takes an occurrence id ("<schedule_id>:<unix seconds>") and returns the time it was
// expected. The id is deterministic, so the expected time never has to be stored twice or guessed.
func expectedFromOccurrenceID(id string) (time.Time, bool) {
	i := strings.LastIndex(id, ":")
	if i < 0 {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(id[i+1:], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(n, 0).UTC(), true
}

func lateness(at time.Time, expected time.Time) *float64 {
	s := at.Sub(expected).Seconds()
	if s < 0 {
		s = 0 // a clock that is slightly behind must not report an occurrence as early
	}
	return &s
}

// scheduleEventFrom takes a stored event and returns the ledger event it implies, or false if it is not one the
// ledger cares about. attempt.started counts only for the FIRST run of an occurrence (a retry is not "when execution
// actually began").
func scheduleEventFrom(e models.EventEnvelope) (ScheduleEvent, bool) {
	switch e.EventType {
	case "schedule.occurrence_due", "schedule.occurrence_run_created", "schedule.occurrence_skipped":
	case "attempt.started":
		if e.ScheduleOccurrenceID == "" || e.RetryIndex != 0 {
			return ScheduleEvent{}, false
		}
	default:
		return ScheduleEvent{}, false
	}
	if e.ScheduleID == "" || e.ScheduleOccurrenceID == "" {
		return ScheduleEvent{}, false
	}

	ev := ScheduleEvent{Type: e.EventType, At: e.OccurredAt, ScheduleID: e.ScheduleID, OccurrenceID: e.ScheduleOccurrenceID}
	if e.EventType == "schedule.occurrence_run_created" || e.EventType == "attempt.started" {
		ev.RunID = e.JobId
	}
	return ev, true
}

// ReduceOccurrence takes the current ledger row for an occurrence and one event and returns the new row. Like ReduceRun
// it is pure and order-independent: an outcome only moves from DUE to CREATED or SKIPPED, a recorded lateness is set
// once, and repeating an event changes nothing.
func ReduceOccurrence(cur models.ScheduleOccurrence, e ScheduleEvent) models.ScheduleOccurrence {
	next := cur
	at := e.At.UTC()

	if next.ScheduleID == "" {
		next.ScheduleID = e.ScheduleID
	}
	if next.OccurrenceID == "" {
		next.OccurrenceID = e.OccurrenceID
	}
	if next.ExpectedAt.IsZero() {
		if expected, ok := expectedFromOccurrenceID(e.OccurrenceID); ok {
			next.ExpectedAt = expected
		}
	}
	if next.RecordedAt.IsZero() || at.Before(next.RecordedAt) {
		next.RecordedAt = at
	}
	if at.After(next.LastEventAt) {
		next.LastEventAt = at
	}

	switch e.Type {
	case "schedule.occurrence_due":
		if next.Outcome == "" {
			next.Outcome = OutcomeDue
		}
	case "schedule.occurrence_skipped":
		if next.Outcome == "" || next.Outcome == OutcomeDue {
			next.Outcome = OutcomeSkipped
		}
	case "schedule.occurrence_run_created", "attempt.started":
		if next.Outcome == "" || next.Outcome == OutcomeDue {
			next.Outcome = OutcomeCreated
		}
		if next.Outcome == OutcomeCreated {
			if next.RunID == "" {
				next.RunID = e.RunID
			}
			if e.Type == "schedule.occurrence_run_created" && next.CreationLatenessSeconds == nil {
				next.CreationLatenessSeconds = lateness(at, next.ExpectedAt)
			}
			if e.Type == "attempt.started" && next.StartLatenessSeconds == nil {
				next.StartLatenessSeconds = lateness(at, next.ExpectedAt)
			}
		}
	}
	return next
}

// SummarizeSchedule takes a schedule's ledger rows and returns its projection, purely. The live writer computes the
// same thing with aggregate queries (so it does not load every row); a test holds the two to be equal.
func SummarizeSchedule(scheduleID string, rows []models.ScheduleOccurrence) models.ScheduleProjection {
	p := models.ScheduleProjection{ScheduleID: scheduleID}
	var latestRun *models.ScheduleOccurrence

	for i := range rows {
		r := rows[i]
		if r.LastEventAt.After(p.LastEventAt) {
			p.LastEventAt = r.LastEventAt
		}
		if r.ExpectedAt.After(p.LastExpectedAt) {
			p.LastExpectedAt, p.LastOccurrenceID = r.ExpectedAt, r.OccurrenceID
		}
		switch r.Outcome {
		case OutcomeCreated:
			p.OccurrencesCreated++
			if latestRun == nil || r.ExpectedAt.After(latestRun.ExpectedAt) {
				latestRun = &rows[i]
			}
		case OutcomeSkipped:
			p.OccurrencesSkipped++
			if p.LastSkippedAt == nil || r.RecordedAt.After(*p.LastSkippedAt) {
				t := r.RecordedAt
				p.LastSkippedAt = &t
			}
		}
	}
	if latestRun != nil {
		p.LastRunID = latestRun.RunID
		p.LastCreationLatenessSeconds = latestRun.CreationLatenessSeconds
		p.LastStartLatenessSeconds = latestRun.StartLatenessSeconds
	}
	return p
}

// summarizeFromDB computes SummarizeSchedule's answer with aggregate queries.
func summarizeFromDB(tx *gorm.DB, scheduleID string) models.ScheduleProjection {
	p := models.ScheduleProjection{ScheduleID: scheduleID}

	var created, skipped int64
	tx.Model(&models.ScheduleOccurrence{}).Where("schedule_id = ? AND outcome = ?", scheduleID, OutcomeCreated).Count(&created)
	tx.Model(&models.ScheduleOccurrence{}).Where("schedule_id = ? AND outcome = ?", scheduleID, OutcomeSkipped).Count(&skipped)
	p.OccurrencesCreated, p.OccurrencesSkipped = int(created), int(skipped)

	var last models.ScheduleOccurrence
	if err := tx.Where("schedule_id = ?", scheduleID).Order("expected_at DESC, id DESC").First(&last).Error; err == nil {
		p.LastExpectedAt, p.LastOccurrenceID = last.ExpectedAt, last.OccurrenceID
	}

	var latestRun models.ScheduleOccurrence
	if err := tx.Where("schedule_id = ? AND outcome = ?", scheduleID, OutcomeCreated).Order("expected_at DESC, id DESC").First(&latestRun).Error; err == nil {
		p.LastRunID = latestRun.RunID
		p.LastCreationLatenessSeconds = latestRun.CreationLatenessSeconds
		p.LastStartLatenessSeconds = latestRun.StartLatenessSeconds
	}

	var lastSkipped models.ScheduleOccurrence
	if err := tx.Where("schedule_id = ? AND outcome = ?", scheduleID, OutcomeSkipped).Order("recorded_at DESC, id DESC").First(&lastSkipped).Error; err == nil {
		t := lastSkipped.RecordedAt
		p.LastSkippedAt = &t
	}

	var newest models.ScheduleOccurrence
	if err := tx.Where("schedule_id = ?", scheduleID).Order("last_event_at DESC, id DESC").First(&newest).Error; err == nil {
		p.LastEventAt = newest.LastEventAt
	}
	return p
}

// saveScheduleProjection writes a schedule's projection, creating the row if needed.
func saveScheduleProjection(tx *gorm.DB, p models.ScheduleProjection) error {
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.ScheduleProjection{ScheduleID: p.ScheduleID}).Error; err != nil {
		return err
	}
	var stored models.ScheduleProjection
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("schedule_id = ?", p.ScheduleID).First(&stored).Error; err != nil {
		return err
	}
	p.ID, p.CreatedAt = stored.ID, stored.CreatedAt
	return tx.Save(&p).Error
}

// UpdateScheduleProjection takes a stored event and, if it is one the occurrence ledger cares about, applies it to that
// occurrence's row (under a lock) and refreshes the schedule's summary. LogEventWith calls it for every event.
func UpdateScheduleProjection(ctx context.Context, e models.EventEnvelope) {
	ev, ok := scheduleEventFrom(e)
	if !ok {
		return
	}

	err := database.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.ScheduleOccurrence{ScheduleID: ev.ScheduleID, OccurrenceID: ev.OccurrenceID}).Error; err != nil {
			return err
		}
		var cur models.ScheduleOccurrence
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("occurrence_id = ?", ev.OccurrenceID).First(&cur).Error; err != nil {
			return err
		}
		if err := tx.Save(ptr(ReduceOccurrence(cur, ev))).Error; err != nil {
			return err
		}
		return saveScheduleProjection(tx, summarizeFromDB(tx, ev.ScheduleID))
	})
	if err != nil {
		fmt.Println("FAILED TO UPDATE SCHEDULE PROJECTION: ", err)
	}
}

func ptr(o models.ScheduleOccurrence) *models.ScheduleOccurrence { return &o }

// LoadScheduleMonitoring takes a schedule id and a limit and returns that schedule's projection (nil if it has none yet)
// and its most recent occurrences, newest first. The schedule detail API calls it.
func LoadScheduleMonitoring(ctx context.Context, scheduleID string, limit int) (*models.ScheduleProjection, []models.ScheduleOccurrence) {
	db := database.DB.WithContext(ctx)

	var projection *models.ScheduleProjection
	var p models.ScheduleProjection
	if err := db.Where("schedule_id = ?", scheduleID).First(&p).Error; err == nil {
		projection = &p
	}

	occurrences := make([]models.ScheduleOccurrence, 0)
	db.Where("schedule_id = ?", scheduleID).Order("expected_at DESC, id DESC").Limit(limit).Find(&occurrences)
	return projection, occurrences
}

// ---------------------------------------------------------------------------
// Rebuild: replay the schedule events, then backfill runs that pre-date them from the tasks table.
// ---------------------------------------------------------------------------

// ScheduleRebuildReport says what a schedule rebuild found. Counts only.
type ScheduleRebuildReport struct {
	Occurrences int // ledger rows implied by events and tasks
	Missing     int // not stored
	Wrong       int // stored, but different
	Schedules   int // schedules whose summary was wrong or missing
}

func floatEq(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	d := *a - *b
	return d < 0.001 && d > -0.001
}

func occurrencesEqual(a, b models.ScheduleOccurrence) bool {
	return a.Outcome == b.Outcome && a.RunID == b.RunID && sameTime(a.ExpectedAt, b.ExpectedAt) &&
		floatEq(a.CreationLatenessSeconds, b.CreationLatenessSeconds) && floatEq(a.StartLatenessSeconds, b.StartLatenessSeconds)
}

func summariesEqual(a, b models.ScheduleProjection) bool {
	skippedEq := (a.LastSkippedAt == nil) == (b.LastSkippedAt == nil) && (a.LastSkippedAt == nil || sameTime(*a.LastSkippedAt, *b.LastSkippedAt))
	return a.LastOccurrenceID == b.LastOccurrenceID && sameTime(a.LastExpectedAt, b.LastExpectedAt) && a.LastRunID == b.LastRunID &&
		a.OccurrencesCreated == b.OccurrencesCreated && a.OccurrencesSkipped == b.OccurrencesSkipped && skippedEq &&
		floatEq(a.LastCreationLatenessSeconds, b.LastCreationLatenessSeconds) && floatEq(a.LastStartLatenessSeconds, b.LastStartLatenessSeconds) &&
		sameTime(a.LastEventAt, b.LastEventAt)
}

// RebuildScheduleMonitoring takes a context and whether to apply, and rebuilds the occurrence ledger and every
// schedule summary: from the schedule events, then (for runs created before those events existed) from the tasks
// table. With apply false it only reports. Idempotent.
func RebuildScheduleMonitoring(ctx context.Context, apply bool) (ScheduleRebuildReport, error) {
	return rebuildScheduleMonitoring(ctx, apply, "")
}

// rebuildScheduleMonitoring is RebuildScheduleMonitoring narrowed to schedule ids starting with prefix (tests).
func rebuildScheduleMonitoring(ctx context.Context, apply bool, prefix string) (ScheduleRebuildReport, error) {
	db := database.DB.WithContext(ctx)
	var report ScheduleRebuildReport

	// 1. replay the events, in EventLess order, through the same reducer
	var evs []models.EventEnvelope
	q := db.Where("event_type IN ? OR (event_type = ? AND schedule_occurrence_id <> '')", []string{"schedule.occurrence_due", "schedule.occurrence_run_created", "schedule.occurrence_skipped"}, "attempt.started")
	if prefix != "" {
		q = q.Where("schedule_id LIKE ?", prefix+"%")
	}
	if err := q.Find(&evs).Error; err != nil {
		return report, err
	}
	SortEvents(evs)

	rows := make(map[string]models.ScheduleOccurrence)
	for _, e := range evs {
		if ev, ok := scheduleEventFrom(e); ok {
			rows[ev.OccurrenceID] = ReduceOccurrence(rows[ev.OccurrenceID], ev)
		}
	}

	// 2. backfill from tasks: a scheduled run that created no occurrence event (it pre-dates them) still happened
	var tasks []models.Task
	tq := db.Where("schedule_id <> ''")
	if prefix != "" {
		tq = tq.Where("schedule_id LIKE ?", prefix+"%")
	}
	if err := tq.Find(&tasks).Error; err != nil {
		return report, err
	}
	for _, task := range tasks {
		if task.ExpectedAt.IsZero() || task.RetryIndex != 0 {
			continue
		}
		id := task.ScheduleOccurrenceId
		if id == "" {
			id = fmt.Sprintf("%s:%d", task.ScheduleId, task.ExpectedAt.UTC().Unix())
		}
		row := rows[id]
		if row.OccurrenceID == "" {
			row = models.ScheduleOccurrence{ScheduleID: task.ScheduleId, OccurrenceID: id, ExpectedAt: task.ExpectedAt.UTC(), Outcome: OutcomeCreated, RecordedAt: task.CreatedAt.UTC(), LastEventAt: task.CreatedAt.UTC()}
		}
		if row.Outcome == OutcomeCreated || row.Outcome == OutcomeDue {
			row.Outcome = OutcomeCreated
			if row.RunID == "" {
				row.RunID = task.JobId
			}
			if row.CreationLatenessSeconds == nil {
				row.CreationLatenessSeconds = lateness(task.CreatedAt, row.ExpectedAt)
			}
			if row.StartLatenessSeconds == nil {
				var p models.RunProjection
				if err := db.Where("job_id = ?", task.JobId).First(&p).Error; err == nil && !p.StartedAt.IsZero() {
					row.StartLatenessSeconds = lateness(p.StartedAt, row.ExpectedAt)
				}
			}
		}
		rows[id] = row
	}
	report.Occurrences = len(rows)

	// 3. compare with what is stored, and apply
	bySchedule := make(map[string][]models.ScheduleOccurrence)
	for id, want := range rows {
		bySchedule[want.ScheduleID] = append(bySchedule[want.ScheduleID], want)

		var stored models.ScheduleOccurrence
		err := db.Unscoped().Where("occurrence_id = ?", id).First(&stored).Error
		switch {
		case err != nil:
			report.Missing++
			if apply {
				if err := db.Create(&want).Error; err != nil {
					return report, err
				}
			}
		case stored.DeletedAt.Valid || !occurrencesEqual(stored, want):
			report.Wrong++
			if apply {
				want.ID, want.CreatedAt = stored.ID, stored.CreatedAt
				if err := db.Unscoped().Save(&want).Error; err != nil {
					return report, err
				}
			}
		}
	}

	for scheduleID, list := range bySchedule {
		want := SummarizeSchedule(scheduleID, list)

		var stored models.ScheduleProjection
		err := db.Where("schedule_id = ?", scheduleID).First(&stored).Error
		if err != nil || !summariesEqual(stored, want) {
			report.Schedules++
			if apply {
				if err := db.Transaction(func(tx *gorm.DB) error { return saveScheduleProjection(tx, want) }); err != nil {
					return report, err
				}
			}
		}
	}
	return report, nil
}

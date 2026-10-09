package models

import (
	"time"

	"gorm.io/gorm"
)

// RFC-005 §7 Schedule Projection and RFC-002 §14 Monitoring Requirements.
//
// RFC-002 §14 asks whether a run was created for EACH expected occurrence, and what should have run recently. A
// skipped occurrence has no task, so before this it existed nowhere. ScheduleOccurrence is the ledger: one row per
// occurrence, written from the schedule events. ScheduleProjection is the per-schedule summary of that ledger.

// ScheduleOccurrence is one expected occurrence of a schedule.
type ScheduleOccurrence struct {
	gorm.Model

	ScheduleID   string `json:"schedule_id" gorm:"index"`
	OccurrenceID string `json:"occurrence_id" gorm:"uniqueIndex"` // "<schedule_id>:<expected unix time>"

	ExpectedAt time.Time `json:"expected_at"`
	Outcome    string    `json:"outcome"` // DUE (announced, no run yet) | CREATED | SKIPPED
	RunID      string    `json:"run_id"`

	// how late the run was CREATED, and how late its first attempt STARTED, both measured from expected_at using the
	// events' own times (nil until known)
	CreationLatenessSeconds *float64 `json:"creation_lateness_seconds"`
	StartLatenessSeconds    *float64 `json:"start_lateness_seconds"`

	RecordedAt  time.Time `json:"recorded_at"`
	LastEventAt time.Time `json:"last_event_at"`
}

// ScheduleProjection summarises a schedule's ledger.
type ScheduleProjection struct {
	gorm.Model

	ScheduleID string `json:"schedule_id" gorm:"uniqueIndex"`

	LastExpectedAt   time.Time `json:"last_expected_at"`
	LastOccurrenceID string    `json:"last_occurrence_id"`

	// the most recent occurrence that produced a run
	LastRunID                   string   `json:"last_run_id"`
	LastCreationLatenessSeconds *float64 `json:"last_creation_lateness_seconds"`
	LastStartLatenessSeconds    *float64 `json:"last_start_lateness_seconds"`

	OccurrencesCreated int        `json:"occurrences_created"`
	OccurrencesSkipped int        `json:"occurrences_skipped"` // skipped events recorded (capped per fire, see RFC-002 §11)
	LastSkippedAt      *time.Time `json:"last_skipped_at"`

	LastEventAt time.Time `json:"last_event_at"`
}

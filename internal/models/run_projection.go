package models

import (
	"time"

	"gorm.io/gorm"
)

// RFC-005 §7 Projections — Run Projection: current execution state, timestamps, PII summary (implemented here as one row per JobId, upserted on every event)
type RunProjection struct {
	gorm.Model

	JobId string `json:"job_id" gorm:"uniqueIndex"`
	// RFC-005 §8 Fact vs Interpretation: this is the current authoritative state fact, not a derived annotation like RUN_LOST/RUN_STUCK
	CurrentStatus   string    `json:"current_status"`
	QueuedAt        time.Time `json:"queued_at"`
	StartedAt       time.Time `json:"started_at"`
	CompletedAt     time.Time `json:"completed_at"`
	RecoveryStarted bool      `json:"recovery_started"`
	// RFC-005 §7 Run Projection: "PII summary" — count not yet actually populated by UpdateProjection (field exists, not wired in)
	PIIFindingCount int `json:"pii_finding_count"`
	// RFC-003 §12 Reclaim Semantics: "Reclaiming must not automatically mean the previous attempt failed... Monitoring may observe... delivery reclaimed and flag a consistency/anomaly condition."
	WasReclaimed bool      `json:"was_reclaimed"`
	LastEventAt  time.Time `json:"last_event_at"`

	// RFC-005 §7 Run Projection: "latest attempt, attempt count, ... active monitoring annotations, ... alert
	// summary". All of these are RECOMPUTED from their source tables, never incremented, so they cannot drift and
	// replaying an event changes nothing.
	LatestAttemptID       string `json:"latest_attempt_id"`
	LatestAttemptStatus   string `json:"latest_attempt_status"`
	LatestWorkerID        string `json:"latest_worker_id"`
	AttemptCount          int    `json:"attempt_count"`
	ActiveAnnotationCount int    `json:"active_annotation_count"`
	OpenAlertCount        int    `json:"open_alert_count"`

	// RFC-005 §2 "identify missing or contradictory signals": set when a SECOND, DIFFERENT terminal status arrives
	// (completed after failed). The first terminal status is kept.
	Contradicted      bool   `json:"contradicted"`
	ContradictionNote string `json:"contradiction_note"`
}

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
}

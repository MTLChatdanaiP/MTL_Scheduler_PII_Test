package models

import (
	"time"

	"gorm.io/gorm"
)

// RFC-006 §33. A record that somebody did something sensitive.
//
// Reading raw PII back out of the vault is the most sensitive operation in the
// system, and until now nothing recorded who did it or for which job. This holds
// one row per such access, carrying the actor, never the values themselves.
type AuditRecord struct {
	gorm.Model

	Actor       string    `json:"actor"`
	Action      string    `json:"action"`       // e.g. PII_RAW_VALUE_READ
	SubjectType string    `json:"subject_type"` // e.g. JOB
	SubjectID   string    `json:"subject_id"`
	Count       int       `json:"count"` // how many values were returned
	OccurredAt  time.Time `json:"occurred_at"`
}

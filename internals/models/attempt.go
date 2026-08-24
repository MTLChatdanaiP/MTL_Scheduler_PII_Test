package models

import (
	"time"

	"gorm.io/gorm"
)

// RFC-001 §4 Domain Model (Attempt) / §6 Attempt State Model: one worker's
// ownership/execution try within a run — distinct from the run itself.
// Multiple attempts can exist for the same JobId over time (retries via a
// new Task row, or reclaims via the same Task row getting reprocessed).
type Attempt struct {
	gorm.Model

	AttemptId string // ULID, generated fresh each attempt
	JobId     string // links back to the Task
	WorkerId  string
	// RFC-001 §6 Attempt State Model: CREATED -> CLAIMED -> STARTED -> SUCCEEDED|FAILED|ABANDONED
	Status string // "Claimed" -> "Started" -> "Succeeded" | "Failed" | "Abandoned"
	// RFC-001 §8 Invariant 4: "attempt_number is unique within one run" —
	// counted fresh per JobId in ProcessTask before this struct is created
	AttemptNumber   int
	ClaimedAt       time.Time
	StartedAt       time.Time
	FinishedAt      time.Time
	FailureCategory string // empty unless Status == "Failed"
}

package models

import (
	"time"

	"gorm.io/gorm"
)

// RFC-001 §4 Domain Model (JobRun) / PRD §8 Core Job Lifecycle — this struct plays the role of RFC-001's JobRun, collapsed into a single table rather than separate Run/Attempt records
type Task struct {
	gorm.Model
	// PRD §9 Job Identity Requirements — ULID used as the stable, sortable job/run identifier
	JobId    string // job id
	TaskName string // task name
	TaskType string // task type
	Payload  string // payload
	// RFC-001 §5 Run State Model: CREATED -> QUEUED -> RUNNING -> SUCCEEDED|FAILED — simplified here to Pending/Queued/Running/Completed as plain strings rather than the full RFC-001 state machine (see internals/jobstate for an unused draft of the fuller model)
	Status     string    `gorm:"default:Pending"` // Pending, Running, Completed
	FinishedAt time.Time //Time finished
	// RFC-002 §4 Domain Model (ScheduleOccurrence.expected_at) / §7 Scheduling Flow — this project stores the due time directly on Task rather than a separate ScheduleDefinition/ScheduleOccurrence model
	RunAt time.Time
}

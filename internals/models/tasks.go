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

	// RFC-002 §8 Schedule Drift: only meaningfully set for tasks spawned from
	// a ScheduleDefinition; stays zero-value for directly-POSTed tasks
	ExpectedAt time.Time

	// RFC-001 §4 Domain Model (ExecutionChain/JobRun): a policy-level retry
	// creates a NEW Task row rather than mutating this one — these three fields
	// preserve the lineage. ExecutionChainId is shared across a task and all
	// its retries; ParentRunId points to the run that caused this one to exist
	// (empty for the original); RetryIndex increments per retry (0 = root).
	ExecutionChainId string // same value across a task and all its retries
	ParentRunId      string // empty for the original task; set to the failed task's JobId for retries
	RetryIndex       int    // 0 for the original, +1 per retry

	// RFC-001 §12: populated only for Rerun/Replay (new execution_chain_id,
	// references the old run) — never set by the current automatic-retry path,
	// which reuses the same execution_chain_id instead via ParentRunId
	SourceRunId string `json:"source_run_id"`
}

package models

import (
	"time"

	"gorm.io/gorm"
)

// RFC-005 §5 Monitoring Event Envelope — minimum fields: event_id, event_type, schema_version, occurred_at, ingested_at, producer, plus correlation fields
// PRD §28 Event Model
type EventEnvelope struct {
	gorm.Model

	// RFC-000 §5.2 Stable IDs Cross Contexts: correlation key used to join this event back to a task/run
	JobId     string `json:"job_id"`
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	// RFC-000 §5.4 At-Least-Once Is Assumed: "Every cross-context event requires: event_id, producer identity, timestamp, schema version." — schema_version not yet implemented
	SchemaVersion string    `json:"schema_version"`
	OccurredAt    time.Time `json:"occurred_at"`
	IngestedAt    time.Time `json:"ingested_at"`
	Producer      string    `json:"producer"`

	// RFC-001 §4 Domain Model: execution chain / run / attempt lineage fields — commented out, not built (this project models a Task directly rather than Run/Attempt per RFC-001)
	ExecutionChainID string `json:"execution_chain_id"`
	//RunID                string `json:"run_id"`
	ParentRunID string `json:"parent_run_id"`
	RetryIndex  int    `json:"retry_index"`

	// RFC-004 §8 Execution Heartbeat: "attempt_id, worker_id, occurred_at, optional_progress" --
	// only populated on an attempt.heartbeat event; every other event leaves these empty.
	AttemptID string `json:"attempt_id,omitempty"`
	WorkerID  string `json:"worker_id,omitempty"`

	//ScheduleID           string `json:"schedule_id"`
	//ScheduleOccurrenceID string `json:"schedule_occurrence_id"`
	//QueueName            string `json:"queue_name"`
	//TraceID              string `json:"trace_id"`
	//CorrelationID        string `json:"correlation_id"`
}

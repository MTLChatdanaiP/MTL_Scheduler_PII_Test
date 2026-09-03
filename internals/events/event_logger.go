package events

import (
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
	"context"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// LogEvent writes a durable event record for a task lifecycle transition.
// RFC-005 §5 Monitoring Event Envelope: "event_id, event_type, schema_version, occurred_at, ingested_at, producer" (subset implemented here)
// RFC-000 §5.3 Domain Events Are Facts: "Events should represent facts that occurred, not UI instructions."
// PRD §28 Event Model
func LogEvent(ctx context.Context, jobId string, eventType string, producer string) {

	var task models.Task
	// RFC-005 §6 Event Immutability: this row is append-only and is never edited/overwritten after being written
	event := models.EventEnvelope{JobId: jobId,
		EventID:       ulid.Make().String(),
		EventType:     eventType,
		OccurredAt:    time.Now().UTC(),
		Producer:      producer,
		SchemaVersion: "1",
		IngestedAt:    time.Now().UTC(),
	}

	find_error := database.DB.WithContext(ctx).
		Where("job_id = ?", jobId).
		First(&task).Error

	if find_error == nil {
		event.ExecutionChainID = task.ExecutionChainId
		event.ParentRunID = task.ParentRunId
		event.RetryIndex = task.RetryIndex
	}

	write_err := database.DB.WithContext(ctx).Create(&event).Error

	// RFC-005 §4 Core Design: "Monitoring receives immutable observations and builds projections." Every event write keeps the derived projection in sync.
	if write_err != nil {
		fmt.Println("FAILED TO WRITE EVENT: ", write_err)
		// RFC-005 §15 Monitoring Gaps / §19 Metrics: monitoring_event_failures_total is the named metric for exactly this case — not yet implemented
		//add a metric or counter or something idk
	} else {
		UpdateProjection(ctx, jobId, eventType, event.OccurredAt)
	}
}

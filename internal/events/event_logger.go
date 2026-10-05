package events

import (
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/live"
	"MTL_Scheduler_PII_Test/internal/models"
	"context"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// LogEvent writes a durable event record for a task lifecycle transition.
// RFC-005 §5 Monitoring Event Envelope: "event_id, event_type, schema_version, occurred_at, ingested_at, producer" (subset implemented here)
// RFC-000 §5.3 Domain Events Are Facts: "Events should represent facts that occurred, not UI instructions."
// PRD §28 Event Model
// LogExecutionHeartbeat is RFC-004 §8's attempt-level heartbeat: distinct from the
// worker's own liveness heartbeat ("a worker can be alive while one handler is
// stuck"), so a hung handler is observable even though the worker process itself is
// fine. A separate function rather than a new LogEvent parameter, so every existing
// LogEvent call site -- which has nothing to do with execution progress -- is
// completely unaffected by this.
func LogExecutionHeartbeat(ctx context.Context, jobId string, attemptId string, workerId string) {
	event := models.EventEnvelope{
		JobId:         jobId,
		EventID:       ulid.Make().String(),
		EventType:     "attempt.heartbeat",
		OccurredAt:    time.Now().UTC(),
		Producer:      "worker",
		SchemaVersion: "1",
		IngestedAt:    time.Now().UTC(),
		AttemptID:     attemptId,
		WorkerID:      workerId,
	}
	// Deliberately NOT routed through UpdateProjection or live.GlobalHub.Publish the
	// way LogEvent is: a heartbeat changes no run-level fact a projection should
	// reflect, and RFC-010 §19/§20 warn against high-frequency low-value live
	// signals (the same reason task.progress is excluded -- see live.IsNoise). Still
	// written durably, so investigation and freshness checks can see it.
	if err := database.DB.WithContext(ctx).Create(&event).Error; err != nil {
		fmt.Println("FAILED TO WRITE EXECUTION HEARTBEAT: ", err)
		live.CountEventWriteFailure()
	}
}

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
		live.CountEventWriteFailure() // RFC-005 §15/§19 monitoring_event_failures_total
	} else {
		if !live.IsNoise(eventType) {
			live.CountRecorded() // RFC-005 §21: durably recorded; not yet published
		}

		UpdateProjection(ctx, jobId, eventType, event.OccurredAt)

		if !live.IsNoise(eventType) {
			live.GlobalHub.Publish(live.Event{
				ID:      event.ID,
				Type:    eventType,
				Subject: jobId,
				At:      event.OccurredAt,
			})
			live.ObservePublishLag(time.Since(event.OccurredAt)) // RFC-005 §21 "live publisher lag"
		}
	}
}

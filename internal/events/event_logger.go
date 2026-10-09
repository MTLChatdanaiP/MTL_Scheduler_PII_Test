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
	producerID, producerSeq := nextProducerStamp("worker")
	event := models.EventEnvelope{
		JobId:            jobId,
		EventID:          ulid.Make().String(),
		EventType:        "attempt.heartbeat",
		OccurredAt:       time.Now().UTC(),
		Producer:         "worker",
		SchemaVersion:    "1",
		IngestedAt:       time.Now().UTC(),
		AttemptID:        attemptId,
		WorkerID:         workerId,
		ProducerID:       producerID,
		ProducerSequence: producerSeq,
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

// LogEvent records an event about a subject (usually a run id). It keeps its signature and calls
// LogEventWith with no extra context, so every existing caller is unchanged.
func LogEvent(ctx context.Context, jobId string, eventType string, producer string) {
	LogEventWith(ctx, jobId, eventType, producer, EventContext{})
}

// LogEventWith is LogEvent plus the ids the caller knows (RFC-005 §5). The envelope's correlation
// fields are filled from the context, the task (for a run-scoped event) or the subject itself.
func LogEventWith(ctx context.Context, subject string, eventType string, producer string, ec EventContext) {

	// RFC-005 §6 Event Immutability: this row is append-only and is never edited/overwritten after being written
	event := models.EventEnvelope{JobId: subject,
		EventID:              ulid.Make().String(),
		EventType:            eventType,
		OccurredAt:           time.Now().UTC(),
		Producer:             producer,
		SchemaVersion:        "1",
		IngestedAt:           time.Now().UTC(),
		AttemptID:            ec.AttemptID,
		WorkerID:             ec.WorkerID,
		ScheduleID:           ec.ScheduleID,
		ScheduleOccurrenceID: ec.ScheduleOccurrenceID,
		QueueName:            ec.QueueName,
	}

	// RFC-005 §16: a per-process sequence, and the stream message this event belongs to
	event.ProducerID, event.ProducerSequence = nextProducerStamp(producer)
	event.StreamPosition = ec.StreamPosition
	if event.StreamPosition == "" {
		event.StreamPosition = streamPositionFrom(ctx)
	}

	runScoped, taskFound := fillCorrelation(ctx, &event, subject, eventType)

	// what the caller knows beats what the task says, and is the only source when the task does not exist yet
	if ec.ExecutionChainID != "" {
		event.ExecutionChainID, event.CorrelationID = ec.ExecutionChainID, ec.ExecutionChainID
	}
	if ec.TraceID != "" {
		event.TraceID = ec.TraceID
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

		// only an event about a run that exists is projected: nothing is written for "system", a worker, a queue
		// or an event that arrives for a run id with no task
		if runScoped && taskFound {
			UpdateProjection(ctx, subject, eventType, event.OccurredAt)
		}

		// RFC-005 §7 / RFC-002 §14: schedule events feed the occurrence ledger and the schedule projection
		UpdateScheduleProjection(ctx, event)

		if !live.IsNoise(eventType) {
			live.GlobalHub.Publish(live.Event{
				ID:        event.ID,
				Type:      eventType,
				Subject:   subject,
				At:        event.OccurredAt,
				ChainID:   event.ExecutionChainID,
				AttemptID: event.AttemptID,
				Severity:  ec.Severity,
			})
			live.ObservePublishLag(time.Since(event.OccurredAt)) // RFC-005 §21 "live publisher lag"

			// RFC-010 §7: the overview's aggregates may have changed. One summary signal per burst (see live/summary.go).
			live.NotifySummaryChanged()
		}
	}
}

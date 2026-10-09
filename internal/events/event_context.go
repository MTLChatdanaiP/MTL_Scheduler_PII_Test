package events

import (
	"context"
	"strings"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

// RFC-005 §5 Monitoring Event Envelope: "correlation fields where applicable".

// EventContext carries ids a caller knows that cannot be worked out from the subject alone.
// A value given here always wins over one derived from the task or the subject.
type EventContext struct {
	AttemptID            string
	WorkerID             string
	ScheduleID           string
	ScheduleOccurrenceID string
	QueueName            string
	StreamPosition       string // the Redis stream message id this event belongs to (RFC-005 §16)

	// For an event written BEFORE the task row exists (the pre-execution PII scan), which cannot read them from it.
	ExecutionChainID string
	TraceID          string

	// Severity is the alert's severity for an alert.opened event. It is not stored on the event row: it only lets the live hub rank
	// the signal (RFC-010 §20: only critical alerts are in the critical tier).
	Severity string
}

// nonRunPrefixes are event families whose subject is NOT a run: a worker id, a queue name, a
// component instance, a subsystem, an alert id, a schedule id. Looking them up as a task can never
// succeed, and used to cost one failed database query per event.
var nonRunPrefixes = []string{"queue.", "worker.", "component.", "monitoring.", "redis.", "alert.", "retention.", "schedule."}

// isRunScoped takes an event's subject and type and returns whether the subject can be a run.
// An unrecognised family counts as run-scoped: looking it up is the safe default.
func isRunScoped(subject string, eventType string) bool {
	if subject == "system" {
		return false
	}
	// the one schedule event whose subject IS a run: the run an occurrence produced
	if eventType == "schedule.occurrence_run_created" {
		return true
	}
	for _, p := range nonRunPrefixes {
		if strings.HasPrefix(eventType, p) {
			return false
		}
	}
	return true
}

// fillCorrelation takes a half-built event and fills its correlation fields, by reading the task
// once for a run-scoped event, or by reading the subject itself for the others.
func fillCorrelation(ctx context.Context, event *models.EventEnvelope, subject string, eventType string) (runScoped bool, taskFound bool) {
	if isRunScoped(subject, eventType) {
		var task models.Task
		if err := database.DB.WithContext(ctx).Where("job_id = ?", subject).First(&task).Error; err != nil {
			return true, false
		}

		event.ExecutionChainID = task.ExecutionChainId
		event.ParentRunID = task.ParentRunId
		event.RetryIndex = task.RetryIndex
		event.TraceID = task.TraceID
		// the execution chain is what ties every retry of one job together, and it is the
		// correlation id Redis carries (RFC-003 §5), so the two agree
		event.CorrelationID = task.ExecutionChainId

		if event.ScheduleID == "" {
			event.ScheduleID = task.ScheduleId
		}
		if event.ScheduleOccurrenceID == "" {
			event.ScheduleOccurrenceID = task.ScheduleOccurrenceId
		}
		if event.QueueName == "" {
			event.QueueName = task.Queue
		}
		return true, true
	}

	switch {
	case strings.HasPrefix(eventType, "worker."):
		if event.WorkerID == "" {
			event.WorkerID = subject
		}
	case strings.HasPrefix(eventType, "queue."):
		if event.QueueName == "" {
			event.QueueName = subject
		}
	case strings.HasPrefix(eventType, "schedule."):
		if event.ScheduleID == "" {
			event.ScheduleID = subject
		}
	}
	return false, false
}

// attemptContext takes an attempt and returns the event context that identifies it.
func attemptContext(a *models.Attempt) EventContext {
	return EventContext{AttemptID: a.AttemptId, WorkerID: a.WorkerId}
}

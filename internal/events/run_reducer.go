package events

import (
	"fmt"
	"strings"
	"time"

	"MTL_Scheduler_PII_Test/internal/models"
)

// RFC-005 §7 Run Projection, §16 Ordering, §8 Fact vs Interpretation.
//
// The projection used to be written by an upsert whose update clause set current_status to the event's status. That
// status is empty for every event type outside five, so task.failed, task.progress and pii.detected all BLANK it: a
// failed run's status was '' forever, and the dashboard's badge (which reads it) showed nothing. The first event also
// inserted an empty row (the update clause only runs on conflict).
//
// ReduceRun replaces that with a pure function. It reads no clock and does not care what order events arrive in.

// RunEvent is the part of an event the run projection needs.
type RunEvent struct {
	Type string
	At   time.Time
}

const (
	rankPending  = 10
	rankQueued   = 20
	rankRunning  = 30
	rankTerminal = 40
)

// statusRank takes a status and returns how far through its life a run is. An unknown or empty status is 0.
func statusRank(status string) int {
	switch status {
	case "Pending":
		return rankPending
	case "Queued":
		return rankQueued
	case "Running":
		return rankRunning
	case "Completed", "Failed", "Blocked":
		return rankTerminal
	}
	return 0
}

func isTerminal(status string) bool { return statusRank(status) == rankTerminal }

// statusFor takes an event type and returns the status it asserts, or "" if it asserts none (most events say
// nothing about the run's state).
func statusFor(eventType string) string {
	switch eventType {
	case "task.created", "task.rerun_created":
		return "Pending"
	case "task.queued":
		return "Queued"
	case "task.started":
		return "Running"
	case "task.completed":
		return "Completed"
	case "task.failed":
		return "Failed"
	case "task.blocked":
		return "Blocked"
	}
	return ""
}

func earliest(current time.Time, at time.Time) time.Time {
	if current.IsZero() || at.Before(current) {
		return at
	}
	return current
}

// ReduceRun takes the current projection and one event and returns the new projection.
//
// A status event moves the status only FORWARD (Pending < Queued < Running < terminal), so a late task.started after
// task.completed cannot reopen the run. The one deliberate exception is task.recovery_started, which puts a Running
// run back to Queued, and never reopens a terminal one. A second, different terminal status keeps the first and sets
// Contradicted. Timestamps are the event's own occurred_at: queued_at and started_at keep the EARLIEST seen, and
// last_event_at the LATEST. Applying the same event twice, or the same events in any order, gives the same result.
func ReduceRun(cur models.RunProjection, e RunEvent) models.RunProjection {
	next := cur
	at := e.At.UTC()

	if at.After(next.LastEventAt) {
		next.LastEventAt = at
	}

	if e.Type == "task.recovery_started" {
		next.WasReclaimed = true
		next.RecoveryStarted = true
		if !isTerminal(next.CurrentStatus) {
			next.CurrentStatus = "Queued"
		}
		return next
	}

	status := statusFor(e.Type)
	if status == "" {
		return next
	}

	switch status {
	case "Queued":
		next.QueuedAt = earliest(next.QueuedAt, at)
	case "Running":
		next.StartedAt = earliest(next.StartedAt, at)
	case "Completed", "Failed":
		// a terminal event's time is when the run finished; keep the earliest if the event repeats
		next.CompletedAt = earliest(next.CompletedAt, at)
	}

	switch {
	case next.CurrentStatus == "":
		next.CurrentStatus = status
	case isTerminal(next.CurrentStatus) && isTerminal(status) && next.CurrentStatus != status:
		next.Contradicted = true
		note := fmt.Sprintf("%s after %s", strings.TrimPrefix(e.Type, "task."), next.CurrentStatus)
		if !strings.Contains(next.ContradictionNote, note) {
			if next.ContradictionNote != "" {
				next.ContradictionNote += "; "
			}
			next.ContradictionNote += note
		}
	case statusRank(status) > statusRank(next.CurrentStatus):
		next.CurrentStatus = status
	}

	return next
}

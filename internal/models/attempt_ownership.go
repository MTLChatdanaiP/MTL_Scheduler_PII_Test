package models

import "time"

// RFC-001 §6 / RFC-003 §12 / RFC-004 §14: recovering an attempt whose worker died.
//
// The decisions live here, as plain functions with no database access, so they can be
// tested without Redis or Postgres. worker/recovery.go gathers the facts and acts on them.

// OwnerEvidence is what is known about the worker that claimed an attempt.
type OwnerEvidence struct {
	LastHeartbeatAt          *time.Time // newest heartbeat from that worker id (nil = it never sent one)
	CurrentInstanceStartedAt *time.Time // when that worker id's newest instance started (nil = unknown)
}

// AttemptOwnerGone reports whether the process that claimed an attempt can no longer be running it.
//
// "Gone" uses the same definition as RUN_LOST detection: no heartbeat at all, or none inside the
// freshness window. On top of that, a worker id that has been started again since the attempt was
// claimed means the instance that claimed it is gone, even though the new instance's fresh
// heartbeats make the worker id look healthy.
func AttemptOwnerGone(claimedAt time.Time, ev OwnerEvidence, now time.Time, heartbeatWindow time.Duration) bool {
	if ev.CurrentInstanceStartedAt != nil && ev.CurrentInstanceStartedAt.After(claimedAt) {
		return true
	}
	if ev.LastHeartbeatAt == nil {
		return true
	}
	return now.Sub(*ev.LastHeartbeatAt) > heartbeatWindow
}

// RecoveryPlan says what to do with a reclaimed message.
type RecoveryPlan struct {
	Wait    bool // an unfinished attempt still belongs to a live worker: leave the message pending, do not run the task twice
	Abandon bool // every unfinished attempt belongs to a dead worker: mark them abandoned, then reprocess
}

// PlanRecovery decides from the run's status and, for each unfinished attempt, whether its owner is gone.
// With no unfinished attempt there is nothing to abandon: the message is simply reprocessed (the worker
// died before it claimed an attempt). A finished run is left to ProcessTask's own terminal-state guard.
func PlanRecovery(runStatus string, ownerGone []bool) RecoveryPlan {
	if runStatus == "Completed" || runStatus == "Failed" {
		return RecoveryPlan{}
	}
	if len(ownerGone) == 0 {
		return RecoveryPlan{}
	}
	for _, gone := range ownerGone {
		if !gone {
			return RecoveryPlan{Wait: true}
		}
	}
	return RecoveryPlan{Abandon: true}
}

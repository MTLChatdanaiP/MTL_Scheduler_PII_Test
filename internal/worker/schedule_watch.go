package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/events"
	"MTL_Scheduler_PII_Test/internal/models"
)

// RFC-002 §11 Events. "Monitoring should not need to inspect scheduler-internal tables to determine what was
// expected." These are the occurrence events and the definition-change events that make that possible.

// maxSkippedEvents caps how many occurrence_skipped events one fire records. After a long outage a one-second
// schedule could have thousands; the exact total goes in the log line instead.
const maxSkippedEvents = 25

// scheduleSeedMarker is the snapshot row that records "the first run has already seeded everything".
const scheduleSeedMarker = "__seeded__"

// occurrenceID takes a schedule id and the time an occurrence was expected, and returns its id. It is
// deterministic, so the same occurrence always has the same id, and it only holds characters that are safe in a
// log line or an id field.
func occurrenceID(scheduleID string, expected time.Time) string {
	return fmt.Sprintf("%s:%d", scheduleID, expected.UTC().Unix())
}

// skippedCount takes the occurrence that is firing, the current time and the interval, and returns how many
// occurrences were passed over: those that came due after `expected` and no later than `now`.
func skippedCount(expected time.Time, now time.Time, interval time.Duration) int {
	if interval <= 0 || !now.After(expected) {
		return 0
	}
	return int(now.Sub(expected) / interval)
}

// skippedOccurrences returns the times of the occurrences that were passed over, k = 1..skippedCount, at most max
// of them, oldest first.
func skippedOccurrences(expected time.Time, now time.Time, interval time.Duration, max int) []time.Time {
	n := skippedCount(expected, now, interval)
	if n > max {
		n = max
	}
	out := make([]time.Time, 0, n)
	for k := 1; k <= n; k++ {
		out = append(out, expected.Add(time.Duration(k)*interval))
	}
	return out
}

// definitionHash takes a schedule and returns a hash of what DEFINES it. NextRunAt, LastRun-style bookkeeping and
// Enabled are deliberately excluded: the scheduler moves NextRunAt on every fire, and enabling is its own event.
func definitionHash(def models.ScheduleDefinition) string {
	h := sha256.New()
	for _, part := range []string{def.TaskName, def.TaskType, def.Payload, strconv.Itoa(def.IntervalSeconds)} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

type scheduleChange struct {
	Kind string // seed | created | updated | enabled | disabled
	Def  models.ScheduleDefinition
	Snap models.ScheduleSnapshot // the snapshot it was compared to (zero for seed/created)
}

// planScheduleChanges takes every schedule definition, the stored snapshots by schedule id and whether the first
// run has already happened, and returns what to do. It touches no database, so it can be tested exhaustively.
//
// On the very first run every existing schedule is SEEDED silently: announcing them all as "created" would be a
// lie about when they were created.
func planScheduleChanges(defs []models.ScheduleDefinition, snaps map[string]models.ScheduleSnapshot, seeded bool) []scheduleChange {
	var changes []scheduleChange

	for _, def := range defs {
		if !seeded {
			changes = append(changes, scheduleChange{Kind: "seed", Def: def})
			continue
		}

		snap, known := snaps[def.ScheduleId]
		if !known {
			changes = append(changes, scheduleChange{Kind: "created", Def: def})
			continue
		}
		if snap.DefinitionHash != definitionHash(def) {
			changes = append(changes, scheduleChange{Kind: "updated", Def: def, Snap: snap})
		}
		if snap.Enabled != def.Enabled {
			kind := "disabled"
			if def.Enabled {
				kind = "enabled"
			}
			changes = append(changes, scheduleChange{Kind: kind, Def: def, Snap: snap})
		}
	}
	return changes
}

// watchScheduleDefinitions takes a context, compares every schedule definition to its stored snapshot, and
// announces what changed (schedule.created, updated, enabled, disabled). It returns nothing.
//
// Each announcement is guarded by a write that only succeeds for ONE scheduler instance (a unique insert, or an
// update conditional on the old value), so two instances never announce the same change twice. The event time is
// the time of DETECTION (at most one poll after the edit), not the time of the edit.
func watchScheduleDefinitions(ctx context.Context) {
	var defs []models.ScheduleDefinition
	if err := database.DB.WithContext(ctx).Find(&defs).Error; err != nil {
		return
	}

	var stored []models.ScheduleSnapshot
	if err := database.DB.WithContext(ctx).Find(&stored).Error; err != nil {
		return
	}
	snaps := make(map[string]models.ScheduleSnapshot, len(stored))
	for _, s := range stored {
		snaps[s.ScheduleID] = s
	}
	_, seeded := snaps[scheduleSeedMarker]

	for _, change := range planScheduleChanges(defs, snaps, seeded) {
		def := change.Def
		ec := events.EventContext{ScheduleID: def.ScheduleId}

		switch change.Kind {
		case "seed":
			database.DB.WithContext(ctx).Create(&models.ScheduleSnapshot{ScheduleID: def.ScheduleId, DefinitionHash: definitionHash(def), Enabled: def.Enabled})

		case "created":
			// the unique index lets exactly one instance win; the winner announces
			err := database.DB.WithContext(ctx).Create(&models.ScheduleSnapshot{ScheduleID: def.ScheduleId, DefinitionHash: definitionHash(def), Enabled: def.Enabled}).Error
			if err == nil {
				events.LogEventWith(ctx, def.ScheduleId, "schedule.created", "scheduler", ec)
			}

		case "updated":
			res := database.DB.WithContext(ctx).Model(&models.ScheduleSnapshot{}).
				Where("schedule_id = ? AND definition_hash = ?", def.ScheduleId, change.Snap.DefinitionHash).
				Update("definition_hash", definitionHash(def))
			if res.Error == nil && res.RowsAffected == 1 {
				events.LogEventWith(ctx, def.ScheduleId, "schedule.updated", "scheduler", ec)
			}

		case "enabled", "disabled":
			res := database.DB.WithContext(ctx).Model(&models.ScheduleSnapshot{}).
				Where("schedule_id = ? AND enabled = ?", def.ScheduleId, change.Snap.Enabled).
				Update("enabled", def.Enabled)
			if res.Error == nil && res.RowsAffected == 1 {
				events.LogEventWith(ctx, def.ScheduleId, "schedule."+change.Kind, "scheduler", ec)
			}
		}
	}

	if !seeded {
		// recorded even when there were no schedules, so one created later is announced, not seeded
		database.DB.WithContext(ctx).Create(&models.ScheduleSnapshot{ScheduleID: scheduleSeedMarker})
	}
}

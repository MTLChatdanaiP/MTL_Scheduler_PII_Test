package worker

// RFC-002 §11 Events. Before this, of the 7 suggested events only schedule.missed existed, and after downtime
// the scheduler silently skipped every occurrence it missed.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internal/cache"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

var t0 = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// ---------------------------------------------------------------------------
// Pure logic
// ---------------------------------------------------------------------------

func TestOccurrenceID_IsDeterministicAndSafe(t *testing.T) {
	a, b := occurrenceID("SCHED1", t0), occurrenceID("SCHED1", t0)
	if a != b {
		t.Fatal("the same occurrence must always get the same id")
	}
	if occurrenceID("SCHED1", t0) == occurrenceID("SCHED1", t0.Add(time.Minute)) || occurrenceID("SCHED1", t0) == occurrenceID("SCHED2", t0) {
		t.Fatal("different occurrences must get different ids")
	}
	if !cache.ValidTraceID(a) {
		t.Fatalf("the id %q must be safe to carry in logs and id fields", a)
	}
	if !strings.HasPrefix(a, "SCHED1:") {
		t.Fatalf("the id should start with its schedule: %q", a)
	}
}

func TestSkippedCount(t *testing.T) {
	min := time.Minute
	tests := []struct {
		name     string
		now      time.Time
		interval time.Duration
		want     int
	}{
		{"on time", t0, min, 0},
		{"just under one interval late", t0.Add(59 * time.Second), min, 0},
		{"exactly one interval late", t0.Add(min), min, 1},
		{"ten intervals late", t0.Add(10 * min), min, 10},
		{"ten and a half", t0.Add(10*min + 30*time.Second), min, 10},
		{"now is before expected", t0.Add(-min), min, 0},
		{"zero interval", t0.Add(time.Hour), 0, 0},
		{"negative interval", t0.Add(time.Hour), -min, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := skippedCount(t0, tt.now, tt.interval); got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSkippedOccurrences_AreOrderedAndCapped(t *testing.T) {
	got := skippedOccurrences(t0, t0.Add(10*time.Minute), time.Minute, 25)
	if len(got) != 10 || !got[0].Equal(t0.Add(time.Minute)) || !got[9].Equal(t0.Add(10*time.Minute)) {
		t.Fatalf("expected the 10 passed-over times oldest first, got %v", got)
	}

	capped := skippedOccurrences(t0, t0.Add(100*time.Second), time.Second, 25)
	if len(capped) != 25 {
		t.Fatalf("expected the cap of 25, got %d", len(capped))
	}
	if n := skippedOccurrences(t0, t0, time.Minute, 25); len(n) != 0 {
		t.Fatalf("an on-time fire skips nothing, got %d", len(n))
	}
}

func TestDefinitionHash_ChangesOnlyWhenTheDefinitionDoes(t *testing.T) {
	base := models.ScheduleDefinition{ScheduleId: "S", TaskName: "n", TaskType: "t", Payload: "p", IntervalSeconds: 60, NextRunAt: t0, Enabled: true}
	h := definitionHash(base)

	// bookkeeping and the enabled flag are not part of the definition
	moved := base
	moved.NextRunAt = t0.Add(time.Hour)
	moved.Enabled = false
	if definitionHash(moved) != h {
		t.Fatal("moving next_run_at or toggling enabled must not look like a redefinition (the scheduler moves next_run_at on every fire)")
	}

	for name, mod := range map[string]func(*models.ScheduleDefinition){
		"task name": func(d *models.ScheduleDefinition) { d.TaskName = "other" },
		"task type": func(d *models.ScheduleDefinition) { d.TaskType = "other" },
		"payload":   func(d *models.ScheduleDefinition) { d.Payload = "other" },
		"interval":  func(d *models.ScheduleDefinition) { d.IntervalSeconds = 61 },
	} {
		changed := base
		mod(&changed)
		if definitionHash(changed) == h {
			t.Errorf("changing the %s must change the hash", name)
		}
	}

	// the stored snapshot must never hold the payload itself (it can hold PII)
	secret := base
	secret.Payload = "jane.doe@example.com 555-123-4567"
	if got := definitionHash(secret); strings.Contains(got, "jane") || len(got) != 64 {
		t.Fatalf("the hash must be an opaque digest, got %q", got)
	}
}

func TestPlanScheduleChanges(t *testing.T) {
	def := func(id string, interval int, enabled bool) models.ScheduleDefinition {
		return models.ScheduleDefinition{ScheduleId: id, TaskName: "n", TaskType: "t", Payload: "p", IntervalSeconds: interval, Enabled: enabled}
	}
	snapOf := func(d models.ScheduleDefinition) models.ScheduleSnapshot {
		return models.ScheduleSnapshot{ScheduleID: d.ScheduleId, DefinitionHash: definitionHash(d), Enabled: d.Enabled}
	}
	kinds := func(cs []scheduleChange) string {
		var out []string
		for _, c := range cs {
			out = append(out, c.Kind)
		}
		return strings.Join(out, ",")
	}

	a := def("A", 60, true)

	t.Run("the first run seeds silently and announces nothing", func(t *testing.T) {
		got := kinds(planScheduleChanges([]models.ScheduleDefinition{a, def("B", 60, true)}, nil, false))
		if got != "seed,seed" {
			t.Fatalf("got %q, want seed,seed: announcing every existing schedule as created would be a lie", got)
		}
	})
	t.Run("unchanged schedules produce nothing", func(t *testing.T) {
		if got := kinds(planScheduleChanges([]models.ScheduleDefinition{a}, map[string]models.ScheduleSnapshot{"A": snapOf(a)}, true)); got != "" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("a schedule with no snapshot is created", func(t *testing.T) {
		if got := kinds(planScheduleChanges([]models.ScheduleDefinition{a}, map[string]models.ScheduleSnapshot{}, true)); got != "created" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("an edit is an update", func(t *testing.T) {
		edited := def("A", 120, true)
		if got := kinds(planScheduleChanges([]models.ScheduleDefinition{edited}, map[string]models.ScheduleSnapshot{"A": snapOf(a)}, true)); got != "updated" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("disabling and enabling are their own events", func(t *testing.T) {
		off := def("A", 60, false)
		if got := kinds(planScheduleChanges([]models.ScheduleDefinition{off}, map[string]models.ScheduleSnapshot{"A": snapOf(a)}, true)); got != "disabled" {
			t.Fatalf("got %q", got)
		}
		if got := kinds(planScheduleChanges([]models.ScheduleDefinition{a}, map[string]models.ScheduleSnapshot{"A": snapOf(off)}, true)); got != "enabled" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("an edit and a disable together are two events", func(t *testing.T) {
		both := def("A", 120, false)
		if got := kinds(planScheduleChanges([]models.ScheduleDefinition{both}, map[string]models.ScheduleSnapshot{"A": snapOf(a)}, true)); got != "updated,disabled" {
			t.Fatalf("got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Firing, against the real database. Each test fires ONE schedule it created itself.
// ---------------------------------------------------------------------------

func newSchedule(t *testing.T, behind time.Duration, interval int) models.ScheduleDefinition {
	t.Helper()
	def := models.ScheduleDefinition{
		ScheduleId: ulid.Make().String(), TaskName: "sched-events-" + ulid.Make().String(), TaskType: "fail_permanent",
		Payload: `{"k":"v"}`, IntervalSeconds: interval, Enabled: true, NextRunAt: time.Now().UTC().Add(-behind),
	}
	if err := database.DB.Create(&def).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var tasks []models.Task
		database.DB.Where("schedule_id = ?", def.ScheduleId).Find(&tasks)
		for _, k := range tasks {
			cleanupJobRows(k.JobId)
		}
		database.DB.Unscoped().Where("job_id = ?", def.ScheduleId).Delete(&models.EventEnvelope{})
		database.DB.Unscoped().Where("schedule_id = ?", def.ScheduleId).Delete(&models.ScheduleSnapshot{})
		database.DB.Unscoped().Where("schedule_id = ?", def.ScheduleId).Delete(&models.ScheduleDefinition{})
	})
	return def
}

func cleanupJobRows(jobID string) {
	database.DB.Unscoped().Where("job_id = ?", jobID).Delete(&models.EventEnvelope{})
	database.DB.Unscoped().Where("job_id = ?", jobID).Delete(&models.RunProjection{})
	database.DB.Unscoped().Where("job_id = ?", jobID).Delete(&models.PIIRecord{})
	database.DB.Unscoped().Where("job_id = ?", jobID).Delete(&models.Task{})
	database.DB.Unscoped().Where("execution_chain_id = ?", jobID).Delete(&models.ExecutionChain{})
}

func tasksOf(t *testing.T, scheduleID string) []models.Task {
	t.Helper()
	var tasks []models.Task
	database.DB.Where("schedule_id = ?", scheduleID).Find(&tasks)
	return tasks
}

func TestFireSchedule_AnnouncesTheOccurrenceAndTheRunItProduced(t *testing.T) {
	def := newSchedule(t, time.Minute, 60)

	fireSchedule(context.Background(), def)

	tasks := tasksOf(t, def.ScheduleId)
	if len(tasks) != 1 {
		t.Fatalf("expected one run, got %d", len(tasks))
	}
	wantOccurrence := occurrenceID(def.ScheduleId, def.NextRunAt)
	if tasks[0].ScheduleOccurrenceId != wantOccurrence {
		t.Fatalf("the run must carry its occurrence id: got %q want %q", tasks[0].ScheduleOccurrenceId, wantOccurrence)
	}

	if n := eventCount(t, def.ScheduleId, "schedule.occurrence_due"); n != 1 {
		t.Fatalf("expected one occurrence_due, found %d", n)
	}
	if n := eventCount(t, tasks[0].JobId, "schedule.occurrence_run_created"); n != 1 {
		t.Fatalf("expected one occurrence_run_created on the run, found %d", n)
	}

	var runCreated models.EventEnvelope
	database.DB.Where("job_id = ? AND event_type = ?", tasks[0].JobId, "schedule.occurrence_run_created").First(&runCreated)
	if runCreated.ScheduleID != def.ScheduleId || runCreated.ScheduleOccurrenceID != wantOccurrence {
		t.Fatalf("the event must identify its schedule and occurrence: %+v", runCreated)
	}

	// and every later event of the run carries the same occurrence
	var created models.EventEnvelope
	database.DB.Where("job_id = ? AND event_type = ?", tasks[0].JobId, "task.created").First(&created)
	if created.ScheduleOccurrenceID != wantOccurrence {
		t.Fatalf("task.created must carry the occurrence id too: %+v", created)
	}
}

func TestFireSchedule_AfterDowntimeRecordsEveryOccurrenceItSkipped(t *testing.T) {
	def := newSchedule(t, 10*time.Minute, 60) // ten intervals behind

	fireSchedule(context.Background(), def)

	if n := eventCount(t, def.ScheduleId, "schedule.occurrence_skipped"); n != 10 {
		t.Fatalf("10 occurrences were passed over, %d were recorded", n)
	}
	var ids []string
	database.DB.Model(&models.EventEnvelope{}).Where("job_id = ? AND event_type = ?", def.ScheduleId, "schedule.occurrence_skipped").Pluck("schedule_occurrence_id", &ids)
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("occurrence %q was recorded twice", id)
		}
		seen[id] = true
	}
	if len(tasks(t, def)) != 1 {
		t.Fatal("only ONE run is created after downtime; the rest are the skipped ones")
	}
}

func tasks(t *testing.T, def models.ScheduleDefinition) []models.Task {
	return tasksOf(t, def.ScheduleId)
}

func TestFireSchedule_TheSkippedEventsAreCapped(t *testing.T) {
	def := newSchedule(t, 100*time.Second, 1) // about 100 occurrences behind

	fireSchedule(context.Background(), def)

	if n := eventCount(t, def.ScheduleId, "schedule.occurrence_skipped"); n != maxSkippedEvents {
		t.Fatalf("a long outage must not write thousands of events: recorded %d, want the cap of %d", n, maxSkippedEvents)
	}
}

func TestFireSchedule_AnOccurrenceClaimedElsewhereIsNotAnnouncedTwice(t *testing.T) {
	def := newSchedule(t, time.Minute, 60)

	fireSchedule(context.Background(), def)
	fireSchedule(context.Background(), def) // a second scheduler instance holding the same stale row

	if n := eventCount(t, def.ScheduleId, "schedule.occurrence_due"); n != 1 {
		t.Fatalf("each occurrence is announced once, found %d", n)
	}
	if len(tasks(t, def)) != 1 {
		t.Fatal("and produces one run")
	}
}

// ---------------------------------------------------------------------------
// Definition changes, detected
// ---------------------------------------------------------------------------

func TestWatchScheduleDefinitions_AnnouncesEachChangeExactlyOnce(t *testing.T) {
	ctx := context.Background()
	watchScheduleDefinitions(ctx) // make sure the first-run seeding has happened, so a new schedule is announced

	def := newSchedule(t, -time.Hour, 60) // due in an hour, so nothing else fires it

	count := func(eventType string) int64 { return eventCount(t, def.ScheduleId, eventType) }

	watchScheduleDefinitions(ctx)
	if count("schedule.created") != 1 {
		t.Fatalf("a new schedule must be announced as created, found %d", count("schedule.created"))
	}
	watchScheduleDefinitions(ctx)
	if count("schedule.created") != 1 || count("schedule.updated") != 0 {
		t.Fatal("polling again with nothing changed must announce nothing")
	}

	database.DB.Model(&models.ScheduleDefinition{}).Where("schedule_id = ?", def.ScheduleId).Update("interval_seconds", 120)
	watchScheduleDefinitions(ctx)
	watchScheduleDefinitions(ctx)
	if count("schedule.updated") != 1 {
		t.Fatalf("an edited interval is announced once, found %d", count("schedule.updated"))
	}

	database.DB.Model(&models.ScheduleDefinition{}).Where("schedule_id = ?", def.ScheduleId).Update("enabled", false)
	watchScheduleDefinitions(ctx)
	if count("schedule.disabled") != 1 {
		t.Fatalf("disabling is announced once, found %d", count("schedule.disabled"))
	}

	database.DB.Model(&models.ScheduleDefinition{}).Where("schedule_id = ?", def.ScheduleId).Update("enabled", true)
	watchScheduleDefinitions(ctx)
	if count("schedule.enabled") != 1 {
		t.Fatalf("enabling is announced once, found %d", count("schedule.enabled"))
	}

	// the scheduler moving next_run_at is bookkeeping, not an edit
	database.DB.Model(&models.ScheduleDefinition{}).Where("schedule_id = ?", def.ScheduleId).Update("next_run_at", time.Now().Add(2*time.Hour))
	watchScheduleDefinitions(ctx)
	if count("schedule.updated") != 1 {
		t.Fatal("moving next_run_at must not be announced as an update")
	}

	var e models.EventEnvelope
	database.DB.Where("job_id = ? AND event_type = ?", def.ScheduleId, "schedule.created").First(&e)
	if e.ScheduleID != def.ScheduleId {
		t.Fatalf("the event must carry its schedule id: %+v", e)
	}
}

// RFC-002 §14: the REAL fireSchedule leaves a ledger row for the occurrence it fired and for each one it skipped.
func TestFireSchedule_WritesTheOccurrenceLedger(t *testing.T) {
	def := newSchedule(t, 5*time.Minute, 60) // five intervals behind: one fires, five are passed over

	fireSchedule(context.Background(), def)

	var fired models.ScheduleOccurrence
	if err := database.DB.Where("occurrence_id = ?", occurrenceID(def.ScheduleId, def.NextRunAt)).First(&fired).Error; err != nil {
		t.Fatalf("the fired occurrence is not in the ledger: %v", err)
	}
	if fired.Outcome != "CREATED" || fired.RunID == "" || fired.CreationLatenessSeconds == nil {
		t.Fatalf("the fired occurrence must be CREATED with its run and creation lateness: %+v", fired)
	}

	var skipped int64
	database.DB.Model(&models.ScheduleOccurrence{}).Where("schedule_id = ? AND outcome = ?", def.ScheduleId, "SKIPPED").Count(&skipped)
	if skipped != 5 {
		t.Fatalf("5 occurrences were passed over; %d are in the ledger as SKIPPED", skipped)
	}

	var p models.ScheduleProjection
	database.DB.Where("schedule_id = ?", def.ScheduleId).First(&p)
	if p.OccurrencesCreated != 1 || p.OccurrencesSkipped != 5 || p.LastRunID != fired.RunID {
		t.Fatalf("the schedule summary must agree: %+v", p)
	}
	t.Cleanup(func() {
		database.DB.Unscoped().Where("schedule_id = ?", def.ScheduleId).Delete(&models.ScheduleOccurrence{})
		database.DB.Unscoped().Where("schedule_id = ?", def.ScheduleId).Delete(&models.ScheduleProjection{})
	})
}

package events

// RFC-005 §7 Schedule Projection, RFC-002 §14: was a run created for each expected occurrence, how late was creation, how
// late did execution actually begin.

import (
	"context"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func occ(schedule string, expected time.Time) string { return schedule + ":" + itoa(expected.Unix()) }

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func f(v float64) *float64 { return &v }

// ---------------------------------------------------------------------------
// Pure
// ---------------------------------------------------------------------------

func TestExpectedFromOccurrenceID(t *testing.T) {
	good := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	got, ok := expectedFromOccurrenceID("01HZSCHEDULE:" + itoa(good.Unix()))
	if !ok || !got.Equal(good) {
		t.Fatalf("got %v %v", got, ok)
	}
	if got, ok := expectedFromOccurrenceID("a:b:" + itoa(good.Unix())); !ok || !got.Equal(good) {
		t.Fatal("a schedule id containing a colon must still parse from the LAST colon")
	}
	for _, bad := range []string{"", "no-colon", "x:", "x:notanumber", ":"} {
		if _, ok := expectedFromOccurrenceID(bad); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestScheduleEventFrom_OnlyTheEventsTheLedgerCaresAbout(t *testing.T) {
	mk := func(et string, retry int, sched, occ string) models.EventEnvelope {
		return models.EventEnvelope{EventType: et, RetryIndex: retry, ScheduleID: sched, ScheduleOccurrenceID: occ, JobId: "run-1", OccurredAt: time.Now()}
	}
	for _, et := range []string{"schedule.occurrence_due", "schedule.occurrence_run_created", "schedule.occurrence_skipped"} {
		if _, ok := scheduleEventFrom(mk(et, 0, "S", "S:1")); !ok {
			t.Errorf("%s must feed the ledger", et)
		}
	}
	if ev, ok := scheduleEventFrom(mk("schedule.occurrence_run_created", 0, "S", "S:1")); !ok || ev.RunID != "run-1" {
		t.Fatal("run_created's subject IS the run, so the run id must be carried")
	}
	if _, ok := scheduleEventFrom(mk("attempt.started", 0, "S", "S:1")); !ok {
		t.Fatal("the first attempt of an occurrence's run sets start lateness")
	}
	if _, ok := scheduleEventFrom(mk("attempt.started", 1, "S", "S:1")); ok {
		t.Fatal("a RETRY's attempt is not when execution actually began")
	}
	for _, e := range []models.EventEnvelope{mk("attempt.started", 0, "S", ""), mk("schedule.created", 0, "S", "S:1"), mk("task.created", 0, "S", "S:1"), mk("schedule.occurrence_due", 0, "", "S:1")} {
		if _, ok := scheduleEventFrom(e); ok {
			t.Errorf("%+v must be ignored", e)
		}
	}
}

func TestReduceOccurrence_LifecycleAndLateness(t *testing.T) {
	expected := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	id := occ("S", expected)
	ev := func(typ string, sec int, run string) ScheduleEvent {
		return ScheduleEvent{Type: typ, At: expected.Add(time.Duration(sec) * time.Second), ScheduleID: "S", OccurrenceID: id, RunID: run}
	}

	row := ReduceOccurrence(models.ScheduleOccurrence{}, ev("schedule.occurrence_due", 1, ""))
	if row.Outcome != OutcomeDue || !row.ExpectedAt.Equal(expected) {
		t.Fatalf("due: %+v", row)
	}
	row = ReduceOccurrence(row, ev("schedule.occurrence_run_created", 3, "run-1"))
	if row.Outcome != OutcomeCreated || row.RunID != "run-1" || row.CreationLatenessSeconds == nil || *row.CreationLatenessSeconds != 3 {
		t.Fatalf("run_created must set the outcome, the run and creation lateness (3s): %+v", row)
	}
	row = ReduceOccurrence(row, ev("attempt.started", 12, "run-1"))
	if row.StartLatenessSeconds == nil || *row.StartLatenessSeconds != 12 {
		t.Fatalf("start lateness must be 12s: %+v", row)
	}
	// set once: a later attempt.started does not overwrite it
	if again := ReduceOccurrence(row, ev("attempt.started", 99, "run-1")); *again.StartLatenessSeconds != 12 {
		t.Fatal("start lateness is the FIRST attempt's, and is set once")
	}
}

func TestReduceOccurrence_AnyOrderOfTheSameEventsGivesTheSameRow(t *testing.T) {
	expected := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	id := occ("S", expected)
	events := []ScheduleEvent{
		{Type: "schedule.occurrence_due", At: expected.Add(1 * time.Second), ScheduleID: "S", OccurrenceID: id},
		{Type: "schedule.occurrence_run_created", At: expected.Add(3 * time.Second), ScheduleID: "S", OccurrenceID: id, RunID: "run-1"},
		{Type: "attempt.started", At: expected.Add(12 * time.Second), ScheduleID: "S", OccurrenceID: id, RunID: "run-1"},
	}
	foldAll := func(order []ScheduleEvent) models.ScheduleOccurrence {
		var r models.ScheduleOccurrence
		for _, e := range order {
			r = ReduceOccurrence(r, e)
		}
		return r
	}
	want := foldAll(events)
	orders := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for _, o := range orders {
		got := foldAll([]ScheduleEvent{events[o[0]], events[o[1]], events[o[2]]})
		if got.Outcome != want.Outcome || got.RunID != want.RunID || !floatEq(got.CreationLatenessSeconds, want.CreationLatenessSeconds) || !floatEq(got.StartLatenessSeconds, want.StartLatenessSeconds) {
			t.Fatalf("order %v gave %+v, want %+v", o, got, want)
		}
	}
}

func TestReduceOccurrence_ASkippedOccurrenceStaysSkippedAndNeverGetsARun(t *testing.T) {
	expected := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	id := occ("S", expected)
	r := ReduceOccurrence(models.ScheduleOccurrence{}, ScheduleEvent{Type: "schedule.occurrence_skipped", At: expected.Add(time.Minute), ScheduleID: "S", OccurrenceID: id})
	if r.Outcome != OutcomeSkipped || r.RunID != "" {
		t.Fatalf("%+v", r)
	}
	if r2 := ReduceOccurrence(r, ScheduleEvent{Type: "schedule.occurrence_due", At: expected, ScheduleID: "S", OccurrenceID: id}); r2.Outcome != OutcomeSkipped {
		t.Fatal("a later due event must not un-skip it")
	}
}

func TestReduceOccurrence_ALateClockNeverReportsNegativeLateness(t *testing.T) {
	expected := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	r := ReduceOccurrence(models.ScheduleOccurrence{}, ScheduleEvent{Type: "schedule.occurrence_run_created", At: expected.Add(-5 * time.Second), ScheduleID: "S", OccurrenceID: occ("S", expected), RunID: "r"})
	if *r.CreationLatenessSeconds != 0 {
		t.Fatalf("a slightly slow clock must not report an occurrence as early, got %v", *r.CreationLatenessSeconds)
	}
}

func TestSummarizeSchedule(t *testing.T) {
	e1 := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	e2, e3 := e1.Add(time.Minute), e1.Add(2*time.Minute)
	rows := []models.ScheduleOccurrence{
		{ScheduleID: "S", OccurrenceID: occ("S", e1), ExpectedAt: e1, Outcome: OutcomeCreated, RunID: "r1", CreationLatenessSeconds: f(1), StartLatenessSeconds: f(2), RecordedAt: e1, LastEventAt: e1.Add(2 * time.Second)},
		{ScheduleID: "S", OccurrenceID: occ("S", e2), ExpectedAt: e2, Outcome: OutcomeSkipped, RecordedAt: e2.Add(time.Second), LastEventAt: e2.Add(time.Second)},
		{ScheduleID: "S", OccurrenceID: occ("S", e3), ExpectedAt: e3, Outcome: OutcomeCreated, RunID: "r3", CreationLatenessSeconds: f(4), StartLatenessSeconds: f(9), RecordedAt: e3, LastEventAt: e3.Add(9 * time.Second)},
	}
	p := SummarizeSchedule("S", rows)

	if p.OccurrencesCreated != 2 || p.OccurrencesSkipped != 1 || p.LastRunID != "r3" || p.LastOccurrenceID != occ("S", e3) {
		t.Fatalf("summary wrong: %+v", p)
	}
	if *p.LastCreationLatenessSeconds != 4 || *p.LastStartLatenessSeconds != 9 || p.LastSkippedAt == nil || !p.LastEventAt.Equal(e3.Add(9*time.Second)) {
		t.Fatalf("the latest run's lateness must be reported: %+v", p)
	}
}

// ---------------------------------------------------------------------------
// Against the real database
// ---------------------------------------------------------------------------

func newScheduleID() string { return "sp-" + ulid.Make().String() }

func forgetSchedule(t *testing.T, scheduleID string) {
	t.Cleanup(func() {
		database.DB.Unscoped().Where("schedule_id = ?", scheduleID).Delete(&models.ScheduleOccurrence{})
		database.DB.Unscoped().Where("schedule_id = ?", scheduleID).Delete(&models.ScheduleProjection{})
		database.DB.Unscoped().Where("job_id = ?", scheduleID).Delete(&models.EventEnvelope{})
		database.DB.Unscoped().Where("schedule_id = ?", scheduleID).Delete(&models.EventEnvelope{})
	})
}

func ledger(t *testing.T, occurrenceID string) models.ScheduleOccurrence {
	t.Helper()
	var r models.ScheduleOccurrence
	if err := database.DB.Where("occurrence_id = ?", occurrenceID).First(&r).Error; err != nil {
		t.Fatalf("no ledger row %s: %v", occurrenceID, err)
	}
	return r
}

func TestScheduleLedger_TheFireEventsWriteTheRowAndTheSummary(t *testing.T) {
	ctx := context.Background()
	scheduleID := newScheduleID()
	forgetSchedule(t, scheduleID)
	expected := time.Now().UTC().Add(-30 * time.Second).Truncate(time.Second)
	id := occ(scheduleID, expected)
	ec := EventContext{ScheduleID: scheduleID, ScheduleOccurrenceID: id}

	run := makeTask(t, func(k *models.Task) { k.ScheduleId = scheduleID; k.ScheduleOccurrenceId = id; k.ExpectedAt = expected })

	LogEventWith(ctx, scheduleID, "schedule.occurrence_due", "scheduler", ec)
	LogEventWith(ctx, run.JobId, "schedule.occurrence_run_created", "scheduler", ec)
	LogEvent(ctx, run.JobId, "attempt.started", "worker")

	row := ledger(t, id)
	if row.Outcome != OutcomeCreated || row.RunID != run.JobId {
		t.Fatalf("the occurrence must be CREATED with its run: %+v", row)
	}
	if row.CreationLatenessSeconds == nil || *row.CreationLatenessSeconds < 29 || *row.CreationLatenessSeconds > 40 {
		t.Fatalf("creation lateness should be about 30s (event time minus expected), got %v", row.CreationLatenessSeconds)
	}
	if row.StartLatenessSeconds == nil || *row.StartLatenessSeconds < 29 {
		t.Fatalf("start lateness is set by the first attempt.started: %v", row.StartLatenessSeconds)
	}

	var p models.ScheduleProjection
	if err := database.DB.Where("schedule_id = ?", scheduleID).First(&p).Error; err != nil {
		t.Fatal(err)
	}
	if p.OccurrencesCreated != 1 || p.LastRunID != run.JobId || p.LastOccurrenceID != id {
		t.Fatalf("the schedule summary must follow: %+v", p)
	}
}

func TestScheduleLedger_ASkippedOccurrenceExistsEvenThoughItHasNoTask(t *testing.T) {
	ctx := context.Background()
	scheduleID := newScheduleID()
	forgetSchedule(t, scheduleID)
	expected := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

	for k := 1; k <= 3; k++ {
		id := occ(scheduleID, expected.Add(time.Duration(k)*time.Minute))
		LogEventWith(ctx, scheduleID, "schedule.occurrence_skipped", "scheduler", EventContext{ScheduleID: scheduleID, ScheduleOccurrenceID: id})
	}

	var n int64
	database.DB.Model(&models.ScheduleOccurrence{}).Where("schedule_id = ? AND outcome = ? AND run_id = ''", scheduleID, OutcomeSkipped).Count(&n)
	if n != 3 {
		t.Fatalf("RFC-002 §14: a skipped occurrence has no run and used to exist nowhere; %d of 3 are in the ledger", n)
	}
	var p models.ScheduleProjection
	database.DB.Where("schedule_id = ?", scheduleID).First(&p)
	if p.OccurrencesSkipped != 3 || p.LastSkippedAt == nil {
		t.Fatalf("%+v", p)
	}
}

func TestScheduleLedger_ReplayingAnEventChangesNothing(t *testing.T) {
	ctx := context.Background()
	scheduleID := newScheduleID()
	forgetSchedule(t, scheduleID)
	id := occ(scheduleID, time.Now().UTC().Add(-time.Minute).Truncate(time.Second))
	ec := EventContext{ScheduleID: scheduleID, ScheduleOccurrenceID: id}

	for i := 0; i < 3; i++ {
		LogEventWith(ctx, scheduleID, "schedule.occurrence_skipped", "scheduler", ec)
	}

	var n int64
	database.DB.Model(&models.ScheduleOccurrence{}).Where("occurrence_id = ?", id).Count(&n)
	var p models.ScheduleProjection
	database.DB.Where("schedule_id = ?", scheduleID).First(&p)
	if n != 1 || p.OccurrencesSkipped != 1 {
		t.Fatalf("at-least-once means a repeat; the ledger must hold ONE row (%d) and count ONE skip (%d)", n, p.OccurrencesSkipped)
	}
}

func TestScheduleLedger_TheAggregateQuerySummaryEqualsThePureSummary(t *testing.T) {
	ctx := context.Background()
	scheduleID := newScheduleID()
	forgetSchedule(t, scheduleID)
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

	run := makeTask(t, func(k *models.Task) { k.ScheduleId = scheduleID })
	id1, id2 := occ(scheduleID, base.Add(time.Minute)), occ(scheduleID, base.Add(2*time.Minute))
	LogEventWith(ctx, scheduleID, "schedule.occurrence_skipped", "scheduler", EventContext{ScheduleID: scheduleID, ScheduleOccurrenceID: id1})
	LogEventWith(ctx, run.JobId, "schedule.occurrence_run_created", "scheduler", EventContext{ScheduleID: scheduleID, ScheduleOccurrenceID: id2})

	var rows []models.ScheduleOccurrence
	database.DB.Where("schedule_id = ?", scheduleID).Find(&rows)
	pure := SummarizeSchedule(scheduleID, rows)
	live := summarizeFromDB(database.DB, scheduleID)

	if !summariesEqual(pure, live) {
		t.Fatalf("the live writer and the pure summary disagree:\n pure %+v\n live %+v", pure, live)
	}
}

func TestLoadScheduleMonitoring_NewestFirstAndLimited(t *testing.T) {
	ctx := context.Background()
	scheduleID := newScheduleID()
	forgetSchedule(t, scheduleID)
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	for k := 1; k <= 5; k++ {
		LogEventWith(ctx, scheduleID, "schedule.occurrence_skipped", "scheduler", EventContext{ScheduleID: scheduleID, ScheduleOccurrenceID: occ(scheduleID, base.Add(time.Duration(k)*time.Minute))})
	}

	p, list := LoadScheduleMonitoring(ctx, scheduleID, 3)

	if p == nil || len(list) != 3 {
		t.Fatalf("projection=%v, got %d occurrences, want 3", p, len(list))
	}
	if !list[0].ExpectedAt.After(list[1].ExpectedAt) || !list[1].ExpectedAt.After(list[2].ExpectedAt) {
		t.Fatal("occurrences must come newest first")
	}
	if none, empty := LoadScheduleMonitoring(ctx, "no-such-"+ulid.Make().String(), 10); none != nil || len(empty) != 0 {
		t.Fatal("an unknown schedule has no projection and an empty (not nil) list")
	}
}

// ---------------------------------------------------------------------------
// Rebuild
// ---------------------------------------------------------------------------

func TestRebuildSchedule_ReplayReproducesTheLiveLedgerExactly(t *testing.T) {
	ctx := context.Background()
	scheduleID := newScheduleID()
	forgetSchedule(t, scheduleID)
	expected := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	id := occ(scheduleID, expected)
	run := makeTask(t, func(k *models.Task) { k.ScheduleId = scheduleID; k.ScheduleOccurrenceId = id; k.ExpectedAt = expected })
	ec := EventContext{ScheduleID: scheduleID, ScheduleOccurrenceID: id}
	LogEventWith(ctx, scheduleID, "schedule.occurrence_due", "scheduler", ec)
	LogEventWith(ctx, run.JobId, "schedule.occurrence_run_created", "scheduler", ec)
	LogEvent(ctx, run.JobId, "attempt.started", "worker")

	report, err := rebuildScheduleMonitoring(ctx, false, scheduleID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Missing != 0 || report.Wrong != 0 || report.Schedules != 0 {
		t.Fatalf("a rebuild from events must equal the live ledger, got %+v", report)
	}
}

func TestRebuildSchedule_RepairsACorruptLedgerAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	scheduleID := newScheduleID()
	forgetSchedule(t, scheduleID)
	expected := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	id := occ(scheduleID, expected)
	LogEventWith(ctx, scheduleID, "schedule.occurrence_skipped", "scheduler", EventContext{ScheduleID: scheduleID, ScheduleOccurrenceID: id})
	database.DB.Model(&models.ScheduleOccurrence{}).Where("occurrence_id = ?", id).UpdateColumn("outcome", OutcomeDue)
	database.DB.Model(&models.ScheduleProjection{}).Where("schedule_id = ?", scheduleID).UpdateColumn("occurrences_skipped", 7)

	check, _ := rebuildScheduleMonitoring(ctx, false, scheduleID)
	if check.Wrong != 1 || check.Schedules != 1 {
		t.Fatalf("check mode must find the wrong row and summary: %+v", check)
	}
	if ledger(t, id).Outcome != OutcomeDue {
		t.Fatal("check mode must change nothing")
	}

	rebuildScheduleMonitoring(ctx, true, scheduleID)
	if ledger(t, id).Outcome != OutcomeSkipped {
		t.Fatal("the outcome must be repaired from the events")
	}
	var p models.ScheduleProjection
	database.DB.Where("schedule_id = ?", scheduleID).First(&p)
	if p.OccurrencesSkipped != 1 {
		t.Fatalf("the summary must be repaired, got %d", p.OccurrencesSkipped)
	}

	again, _ := rebuildScheduleMonitoring(ctx, true, scheduleID)
	if again.Missing+again.Wrong+again.Schedules != 0 {
		t.Fatalf("a second rebuild must change nothing: %+v", again)
	}
}

func TestRebuildSchedule_BackfillsRunsThatPredateTheEventsFromTheTasksTable(t *testing.T) {
	ctx := context.Background()
	scheduleID := newScheduleID()
	forgetSchedule(t, scheduleID)
	expected := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

	// a scheduled run from before Batch 3: it has a schedule id and an expected time, but no occurrence id and no events
	old := makeTask(t, func(k *models.Task) { k.ScheduleId = scheduleID; k.ExpectedAt = expected })
	// a retry child must not count as its own occurrence
	makeTask(t, func(k *models.Task) { k.ScheduleId = scheduleID; k.ExpectedAt = expected; k.RetryIndex = 1 })

	report, _ := rebuildScheduleMonitoring(ctx, true, scheduleID)

	if report.Missing != 1 {
		t.Fatalf("exactly one occurrence should be backfilled (the retry is not one): %+v", report)
	}
	row := ledger(t, occ(scheduleID, expected))
	if row.Outcome != OutcomeCreated || row.RunID != old.JobId || row.CreationLatenessSeconds == nil {
		t.Fatalf("the old run must appear in the ledger as CREATED with its lateness: %+v", row)
	}
}

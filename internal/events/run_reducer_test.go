package events

// RFC-005 §7 Run Projection, §16 Ordering. Pure: no database, no clock.

import (
	"testing"
	"time"

	"MTL_Scheduler_PII_Test/internal/models"
)

var base = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func at(seconds int) time.Time { return base.Add(time.Duration(seconds) * time.Second) }

func fold(events ...RunEvent) models.RunProjection {
	p := models.RunProjection{JobId: "j"}
	for _, e := range events {
		p = ReduceRun(p, e)
	}
	return p
}

func permutations(in []RunEvent) [][]RunEvent {
	if len(in) <= 1 {
		return [][]RunEvent{append([]RunEvent{}, in...)}
	}
	var out [][]RunEvent
	for i := range in {
		rest := append(append([]RunEvent{}, in[:i]...), in[i+1:]...)
		for _, p := range permutations(rest) {
			out = append(out, append([]RunEvent{in[i]}, p...))
		}
	}
	return out
}

// The bug: a failed run's status ended up ” because later events blanked it.
func TestReduceRun_AFailedRunStaysFailedWhateverArrivesAfter(t *testing.T) {
	p := fold(
		RunEvent{"task.created", at(0)}, RunEvent{"task.queued", at(1)}, RunEvent{"task.started", at(2)},
		RunEvent{"task.progress", at(3)}, RunEvent{"pii.detected", at(4)}, RunEvent{"task.failed", at(5)},
		RunEvent{"task.timed_out", at(6)}, RunEvent{"attempt.failed", at(6)}, RunEvent{"task.retries_exhausted", at(7)},
		RunEvent{"alert.opened", at(8)}, RunEvent{"pii.scan_completed", at(9)},
	)
	if p.CurrentStatus != "Failed" {
		t.Fatalf("status = %q, want Failed: events that assert no status must never blank it", p.CurrentStatus)
	}
}

func TestReduceRun_TheFirstEventPopulatesTheRow(t *testing.T) {
	// the old writer inserted an EMPTY row for the first event and ignored its status
	p := fold(RunEvent{"task.started", at(2)})
	if p.CurrentStatus != "Running" || !p.StartedAt.Equal(at(2)) {
		t.Fatalf("the first event must populate the row, got %+v", p)
	}
}

func TestReduceRun_TimestampsAreTheEventsOwnNeverTheClock(t *testing.T) {
	p := fold(RunEvent{"task.queued", at(10)}, RunEvent{"task.started", at(20)}, RunEvent{"task.completed", at(30)})
	if !p.QueuedAt.Equal(at(10)) || !p.StartedAt.Equal(at(20)) || !p.CompletedAt.Equal(at(30)) || !p.LastEventAt.Equal(at(30)) {
		t.Fatalf("times must be the events' occurred_at, got queued=%v started=%v completed=%v last=%v", p.QueuedAt, p.StartedAt, p.CompletedAt, p.LastEventAt)
	}
}

// RFC-005 §16: "Projection logic must not rely solely on wall-clock ordering." Every ordering of the same events must
// give the same projection.
func TestReduceRun_AnyOrderOfTheSameEventsGivesTheSameProjection(t *testing.T) {
	events := []RunEvent{
		{"task.created", at(0)}, {"task.queued", at(1)}, {"task.started", at(2)},
		{"task.progress", at(3)}, {"task.completed", at(4)}, {"pii.detected", at(5)},
	}
	want := fold(events...)

	n := 0
	for _, order := range permutations(events) {
		got := fold(order...)
		n++
		if got.CurrentStatus != want.CurrentStatus || !got.QueuedAt.Equal(want.QueuedAt) || !got.StartedAt.Equal(want.StartedAt) ||
			!got.CompletedAt.Equal(want.CompletedAt) || !got.LastEventAt.Equal(want.LastEventAt) || got.Contradicted {
			t.Fatalf("order %v gave a different projection:\n got  %+v\n want %+v", order, got, want)
		}
	}
	if n != 720 {
		t.Fatalf("expected all 720 orderings to be checked, did %d", n)
	}
}

func TestReduceRun_ALateStartedNeverReopensAFinishedRun(t *testing.T) {
	p := fold(RunEvent{"task.completed", at(10)}, RunEvent{"task.started", at(5)}, RunEvent{"task.queued", at(1)}, RunEvent{"task.created", at(0)})
	if p.CurrentStatus != "Completed" {
		t.Fatalf("a late task.started after task.completed must not regress the status, got %q", p.CurrentStatus)
	}
	// but it still fills in what it knows
	if !p.StartedAt.Equal(at(5)) || !p.QueuedAt.Equal(at(1)) {
		t.Fatalf("late events should still record their times: %+v", p)
	}
}

func TestReduceRun_ASecondDifferentTerminalStatusIsFlaggedAndTheFirstKept(t *testing.T) {
	p := fold(RunEvent{"task.failed", at(5)}, RunEvent{"task.completed", at(6)})
	if p.CurrentStatus != "Failed" || !p.Contradicted || p.ContradictionNote != "completed after Failed" {
		t.Fatalf("expected Failed kept, contradicted, note 'completed after Failed', got %q / %v / %q", p.CurrentStatus, p.Contradicted, p.ContradictionNote)
	}

	q := fold(RunEvent{"task.completed", at(5)}, RunEvent{"task.failed", at(6)})
	if q.CurrentStatus != "Completed" || !q.Contradicted {
		t.Fatalf("the first terminal status wins either way, got %q / %v", q.CurrentStatus, q.Contradicted)
	}

	// a repeated identical terminal event is not a contradiction
	if r := fold(RunEvent{"task.failed", at(5)}, RunEvent{"task.failed", at(6)}); r.Contradicted {
		t.Fatal("the same terminal status twice is a duplicate, not a contradiction")
	}
}

func TestReduceRun_BlockedIsTerminalWhateverTheOrder(t *testing.T) {
	for _, order := range [][]RunEvent{
		{{"task.created", at(0)}, {"task.blocked", at(1)}},
		{{"task.blocked", at(1)}, {"task.created", at(0)}},
	} {
		if p := fold(order...); p.CurrentStatus != "Blocked" || p.Contradicted {
			t.Fatalf("order %v: got %q contradicted=%v, want Blocked", order, p.CurrentStatus, p.Contradicted)
		}
	}
}

func TestReduceRun_RecoveryPutsARunningRunBackToQueuedButNeverReopensAFinishedOne(t *testing.T) {
	running := fold(RunEvent{"task.started", at(2)}, RunEvent{"task.recovery_started", at(3)})
	if running.CurrentStatus != "Queued" || !running.WasReclaimed || !running.RecoveryStarted {
		t.Fatalf("recovery is the one deliberate step back: %+v", running)
	}
	done := fold(RunEvent{"task.completed", at(5)}, RunEvent{"task.recovery_started", at(6)})
	if done.CurrentStatus != "Completed" || !done.WasReclaimed {
		t.Fatalf("a terminal run is never reopened by recovery: %+v", done)
	}
}

func TestReduceRun_ReplayingAnEventChangesNothing(t *testing.T) {
	events := []RunEvent{{"task.created", at(0)}, {"task.queued", at(1)}, {"task.started", at(2)}, {"task.failed", at(3)}}
	once := fold(events...)
	twice := fold(append(append([]RunEvent{}, events...), events...)...)

	if once.CurrentStatus != twice.CurrentStatus || !once.StartedAt.Equal(twice.StartedAt) || !once.CompletedAt.Equal(twice.CompletedAt) || twice.Contradicted {
		t.Fatalf("at-least-once delivery means events repeat; a repeat must change nothing:\n once  %+v\n twice %+v", once, twice)
	}
}

func TestStatusFor_OnlyLifecycleEventsAssertAStatus(t *testing.T) {
	asserting := map[string]string{
		"task.created": "Pending", "task.rerun_created": "Pending", "task.queued": "Queued", "task.started": "Running",
		"task.completed": "Completed", "task.failed": "Failed", "task.blocked": "Blocked",
	}
	for et, want := range asserting {
		if got := statusFor(et); got != want {
			t.Errorf("statusFor(%q) = %q, want %q", et, got, want)
		}
	}
	for _, et := range []string{"task.progress", "task.published", "task.timed_out", "task.retries_exhausted", "pii.detected", "attempt.started", "alert.opened", "schedule.occurrence_due", ""} {
		if got := statusFor(et); got != "" {
			t.Errorf("%q asserts no status but statusFor says %q", et, got)
		}
	}
}

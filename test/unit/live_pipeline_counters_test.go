package unit_test

import (
	"testing"
	"time"

	"MTL_Scheduler_PII_Test/internal/live"
)

// RFC-005 §21: the "live-change outbox backlog" is events recorded but not yet published.
func TestLiveBacklog(t *testing.T) {
	cases := []struct{ recorded, published, want int64 }{
		{0, 0, 0},
		{5, 5, 0}, // everything recorded was published
		{7, 5, 2}, // two recorded, not yet published
		{3, 9, 0}, // never negative (a publish from somewhere else, or a read mid-update)
	}
	for _, c := range cases {
		if got := live.Backlog(c.recorded, c.published); got != c.want {
			t.Errorf("Backlog(%d, %d) = %d, want %d", c.recorded, c.published, got, c.want)
		}
	}
}

func TestPipelineCountersAreExposedInStats(t *testing.T) {
	before := live.GlobalHub.Stats()

	live.CountRecorded()
	live.CountRecorded()
	live.CountEventWriteFailure()

	after := live.GlobalHub.Stats()
	if got := after.EventsRecordedTotal - before.EventsRecordedTotal; got != 2 {
		t.Errorf("events recorded delta = %d, want 2", got)
	}
	if got := after.EventWriteFailuresTotal - before.EventWriteFailuresTotal; got != 1 {
		t.Errorf("event write failures delta = %d, want 1", got)
	}
}

func TestPublishLagTracksLastAndMaxAndClampsNegative(t *testing.T) {
	live.ObservePublishLag(30 * time.Millisecond)
	live.ObservePublishLag(5 * time.Millisecond)

	s := live.GlobalHub.Stats()
	if s.LastPublishLagMs != 5 {
		t.Errorf("last publish lag = %d, want 5", s.LastPublishLagMs)
	}
	if s.MaxPublishLagMs < 30 {
		t.Errorf("max publish lag = %d, want at least 30", s.MaxPublishLagMs)
	}

	live.ObservePublishLag(-10 * time.Millisecond) // clock skew
	if got := live.GlobalHub.Stats().LastPublishLagMs; got != 0 {
		t.Errorf("negative lag should clamp to 0, got %d", got)
	}
}

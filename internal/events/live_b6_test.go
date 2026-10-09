package events

import (
	"context"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/live"
	"MTL_Scheduler_PII_Test/internal/models"
)

// Batch 6: what LogEventWith hands to the live hub (RFC-010 §10 ids, §20 severity, §7 summary).

func nextHubEvent(t *testing.T, ch chan live.Event, wantType string) live.Event {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Type == wantType {
				return e
			}
		case <-timeout:
			t.Fatalf("no %s event reached the hub", wantType)
		}
	}
}

func TestLogEventWith_HandsTheHubTheChainAttemptAndSeverity(t *testing.T) {
	subject := "b6-run-" + ulid.Make().String()
	ch := live.GlobalHub.Subscribe()
	defer live.GlobalHub.Unsubscribe(ch)
	t.Cleanup(func() { database.DB.Unscoped().Where("job_id = ?", subject).Delete(&models.EventEnvelope{}) })

	LogEventWith(context.Background(), subject, "alert.opened", "test", EventContext{
		ExecutionChainID: "chain-b6", AttemptID: "attempt-b6", Severity: "WARNING",
	})

	e := nextHubEvent(t, ch, "alert.opened")
	if e.ChainID != "chain-b6" || e.AttemptID != "attempt-b6" {
		t.Errorf("the live event must carry the chain and attempt ids, got chain=%q attempt=%q", e.ChainID, e.AttemptID)
	}
	if e.Severity != "WARNING" {
		t.Errorf("the live event must carry the alert severity so the hub can rank it, got %q", e.Severity)
	}
	if live.PriorityOfEvent(e) != live.PriorityNormal {
		t.Error("a WARNING alert.opened is NORMAL priority")
	}
}

func TestLogEventWith_ArmsOnePlatformSummaryAfterTheBurst(t *testing.T) {
	prefix := "b6-sum-" + ulid.Make().String()
	hub := live.GlobalHub
	ch := hub.SubscribeMatching(nil, true)
	defer hub.Unsubscribe(ch)
	t.Cleanup(func() { database.DB.Unscoped().Where("job_id LIKE ?", prefix+"%").Delete(&models.EventEnvelope{}) })

	for i := 0; i < 10; i++ {
		LogEvent(context.Background(), prefix+"-"+ulid.Make().String(), "queue.degraded", "test")
	}

	got := nextHubEvent(t, ch, live.TypePlatformSummary)
	if got.ID != 0 || got.Subject != "platform" {
		t.Errorf("summary event = %+v", got)
	}
}

func TestLogEvent_NoiseNeverArmsASummary(t *testing.T) {
	// attempt.heartbeat is written every 10 s per running attempt; it must not make the overview refetch.
	ch := live.GlobalHub.SubscribeMatching(nil, true)
	defer live.GlobalHub.Unsubscribe(ch)
	subject := "b6-noise-" + ulid.Make().String()
	t.Cleanup(func() { database.DB.Unscoped().Where("job_id = ?", subject).Delete(&models.EventEnvelope{}) })

	// drain a summary some earlier test may have armed
	time.Sleep(1300 * time.Millisecond)
	for len(ch) > 0 {
		<-ch
	}

	LogEvent(context.Background(), subject, "task.progress", "test")
	LogExecutionHeartbeat(context.Background(), subject, "att", "w")
	time.Sleep(1300 * time.Millisecond)

	if len(ch) != 0 {
		t.Fatal("noise events must not arm a platform.summary")
	}
}

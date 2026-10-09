package events

// RFC-003 §13: during a Redis outage ReadStream recorded a redis.unavailable event on every failed
// read (one a second), about 3600 rows an hour.

import (
	"context"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func eventsBy(t *testing.T, eventType, producer string) int64 {
	t.Helper()
	var n int64
	database.DB.Model(&models.EventEnvelope{}).Where("event_type = ? AND producer = ?", eventType, producer).Count(&n)
	return n
}

func forgetProducer(t *testing.T, producer string) {
	t.Cleanup(func() { database.DB.Unscoped().Where("producer = ?", producer).Delete(&models.EventEnvelope{}) })
}

func TestLogEventEvery_FiveCallsInOneWindowWriteOneRow(t *testing.T) {
	ResetRateLimitsForTest()
	producer := "ratelimit-" + ulid.Make().String()
	forgetProducer(t, producer)

	for i := 0; i < 5; i++ {
		LogEventEvery(context.Background(), time.Minute, "system", "redis.unavailable", producer)
	}

	if n := eventsBy(t, "redis.unavailable", producer); n != 1 {
		t.Fatalf("five failed reads inside one window should record one event, recorded %d", n)
	}
}

func TestLogEventEvery_EachProducerHasItsOwnWindow(t *testing.T) {
	ResetRateLimitsForTest()
	a, b := "ratelimit-a-"+ulid.Make().String(), "ratelimit-b-"+ulid.Make().String()
	forgetProducer(t, a)
	forgetProducer(t, b)

	LogEventEvery(context.Background(), time.Minute, "system", "redis.unavailable", a)
	LogEventEvery(context.Background(), time.Minute, "system", "redis.unavailable", b)

	if eventsBy(t, "redis.unavailable", a) != 1 || eventsBy(t, "redis.unavailable", b) != 1 {
		t.Fatal("a limit on one producer must not silence another: the worker and the reclaimer both need to be heard")
	}
}

func TestLogEventEvery_RecordsAgainOnceTheWindowHasPassed(t *testing.T) {
	ResetRateLimitsForTest()
	producer := "ratelimit-" + ulid.Make().String()
	forgetProducer(t, producer)

	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = time.Now })

	LogEventEvery(context.Background(), time.Minute, "system", "redis.unavailable", producer)
	now = now.Add(30 * time.Second)
	LogEventEvery(context.Background(), time.Minute, "system", "redis.unavailable", producer) // still inside the window
	now = now.Add(31 * time.Second)
	LogEventEvery(context.Background(), time.Minute, "system", "redis.unavailable", producer) // window has passed

	if n := eventsBy(t, "redis.unavailable", producer); n != 2 {
		t.Fatalf("expected the first and the one after the window, recorded %d", n)
	}
}

func TestLogEventEvery_DifferentEventTypesAreLimitedSeparately(t *testing.T) {
	ResetRateLimitsForTest()
	producer := "ratelimit-" + ulid.Make().String()
	forgetProducer(t, producer)

	LogEventEvery(context.Background(), time.Minute, "system", "redis.unavailable", producer)
	LogEventEvery(context.Background(), time.Minute, "system", "redis.recovered", producer)

	if eventsBy(t, "redis.unavailable", producer) != 1 || eventsBy(t, "redis.recovered", producer) != 1 {
		t.Fatal("limiting one event type must not swallow a different one")
	}
}

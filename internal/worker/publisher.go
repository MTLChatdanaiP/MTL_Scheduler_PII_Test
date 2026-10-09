package worker

import (
	"MTL_Scheduler_PII_Test/internal/cache"
	"MTL_Scheduler_PII_Test/internal/events"

	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// PublishEnvelopeID takes a delivery envelope and returns the Redis stream message id it was published as, or an
// error, by validating it and calling XAdd. The id is what lets monitoring match a PUBLISHED message to the attempt that
// later claims it (RFC-005 §16 stream_position).
//
// RFC-003 §5 Delivery Envelope / §6 Delivery Lifecycle: PUBLISHED -> AVAILABLE stage. It returns the error instead of
// printing it and moving on, so the caller can tell a task was NOT published.
func PublishEnvelopeID(ctx context.Context, env cache.DeliveryEnvelope) (string, error) {
	if err := env.Validate(); err != nil {
		return "", err
	}
	if env.CreatedAt.IsZero() {
		env.CreatedAt = time.Now().UTC()
	}

	id, err := cache.Client.XAdd(ctx, &redis.XAddArgs{
		Stream: cache.TaskStream,
		Values: env.Values(),
	}).Result()

	if err != nil {
		if cache.IsUnavailable(err) {
			events.LogEventEvery(ctx, events.RedisDownEventEvery, "system", "redis.unavailable", "publisher")
		}
		return "", err
	}

	fmt.Printf("Added %s -> stream id %s\n", env.JobID, id)
	return id, nil
}

// PublishEnvelope is PublishEnvelopeID for callers that do not need the id. Its signature is unchanged.
func PublishEnvelope(ctx context.Context, env cache.DeliveryEnvelope) error {
	_, err := PublishEnvelopeID(ctx, env)
	return err
}

// PublishToStream keeps its signature for existing callers. It publishes a minimal envelope
// (the run id alone) and, as before, only reports a failure rather than returning it. New code
// should call PublishEnvelope.
func PublishToStream(ctx context.Context, JobId string) {
	if err := PublishEnvelope(ctx, cache.DeliveryEnvelope{JobID: JobId}); err != nil {
		fmt.Println("XAdd error:", err)
	}
}

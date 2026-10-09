package events

import (
	"context"
	"sort"
	"sync/atomic"

	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internal/models"
)

// RFC-005 §16 Ordering: "retain both occurred_at and ingested_at and, where possible, producer_sequence and
// stream_position. Projection logic must not rely solely on wall-clock ordering."

// producerInstance identifies this process. Generated once, on first use.
var producerInstance = ulid.Make().String()

// producerSeq counts the events this process has written. It restarts at 1 when the process restarts, and the
// producer id (which includes producerInstance) is what tells one run of the process from another.
var producerSeq atomic.Int64

// nextProducerStamp takes the producer name ("api", "worker", "scheduler") and returns the producer id and the next
// sequence number for this process.
func nextProducerStamp(producer string) (string, int64) {
	return producer + ":" + producerInstance, producerSeq.Add(1)
}

type streamPositionKey struct{}

// WithStreamPosition returns a context that carries a Redis stream message id. ProcessStream sets it, so every event
// written while that delivery is being handled (attempt.claimed, started, ...) records which delivery it belonged to,
// without ProcessTask's signature having to change.
func WithStreamPosition(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, streamPositionKey{}, id)
}

func streamPositionFrom(ctx context.Context) string {
	id, _ := ctx.Value(streamPositionKey{}).(string)
	return id
}

// EventLess is the ordering of events for display and replay: occurred_at first; for EQUAL times the producer id, then
// that producer's sequence, then the database id. It is a TOTAL order (a plain tuple comparison), so it cannot produce a
// cycle. Wall-clock time still leads, because events from different processes can only be compared by it; the sequence
// is the authoritative order WITHIN one process, for investigation and for breaking ties. Projections do not depend on
// this ordering at all (see ReduceRun), which is the point of RFC-005 §16.
func EventLess(a, b models.EventEnvelope) bool {
	if !a.OccurredAt.Equal(b.OccurredAt) {
		return a.OccurredAt.Before(b.OccurredAt)
	}
	if a.ProducerID != b.ProducerID {
		return a.ProducerID < b.ProducerID
	}
	if a.ProducerSequence != b.ProducerSequence {
		return a.ProducerSequence < b.ProducerSequence
	}
	return a.ID < b.ID
}

// SortEvents sorts events by EventLess, in place.
func SortEvents(events []models.EventEnvelope) {
	sort.SliceStable(events, func(i, j int) bool { return EventLess(events[i], events[j]) })
}

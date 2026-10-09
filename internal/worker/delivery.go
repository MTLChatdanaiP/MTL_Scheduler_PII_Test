package worker

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/redis/go-redis/v9"

	"MTL_Scheduler_PII_Test/internal/cache"
	"MTL_Scheduler_PII_Test/internal/events"
)

// RFC-003 §14 PII Considerations: "debug tooling must not casually expose entire messages."

// safeField takes a message's values and a key and returns the value only if it is a safe
// id, "-" if it is absent, or "(invalid)" if it is present but not safe to print.
func safeField(values map[string]interface{}, key string) string {
	s, _ := values[key].(string)
	if s == "" {
		return "-"
	}
	if !cache.ValidTraceID(s) {
		return "(invalid)"
	}
	return s
}

// describeMessage takes a Redis message and returns a one-line description holding the
// message id, job id and trace id and NOTHING else, so no field of a richer envelope (or of
// a message somebody wrote by hand) can end up in a log by accident.
func describeMessage(msg redis.XMessage) string {
	return fmt.Sprintf("id=%s job_id=%s trace_id=%s", msg.ID, safeField(msg.Values, "job_id"), safeField(msg.Values, "trace_id"))
}

// ackAndDelete takes a stream, group and message id, acknowledges the message and then
// deletes it from the stream. It returns whether this call was the one that acknowledged it.
//
// RFC-003 §14: "payload retention should be minimized." An acked message is finished work, but
// Redis keeps it until something deletes it, so the stream used to grow forever and its length
// counted every message ever added. After this it holds only work in flight.
//
// The delete happens only if this call's ack acknowledged exactly one entry. If the ack failed,
// or returned 0 (the reclaimer racing the original worker: the other side already acked it and
// deletes it), nothing is deleted.
func ackAndDelete(ctx context.Context, stream string, group string, id string) (bool, error) {
	n, err := cache.Client.XAck(ctx, stream, group, id).Result()
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, nil
	}

	if err := cache.Client.XDel(ctx, stream, id).Err(); err != nil {
		// the ack is what matters; a leftover entry is only untidy
		slog.Warn("acknowledged message could not be deleted", "message_id", id, "error", err)
	}
	return true, nil
}

// dropMalformed takes a delivery that has no usable job_id, and acknowledges and deletes it.
//
// Such a message carries no work. Before this it was never acknowledged, so it sat pending
// forever, kept the oldest-pending age growing and kept the queue looking degraded. Only the
// message id is recorded, never its contents, and the reason never echoes them.
func dropMalformed(ctx context.Context, consumer string, stream string, group string, msg redis.XMessage, reason error) {
	slog.Warn("malformed delivery dropped", "worker_id", consumer, "message_id", msg.ID, "reason", reason.Error())
	events.LogEvent(events.WithStreamPosition(ctx, msg.ID), "system", "delivery.malformed", "worker")

	if _, err := ackAndDelete(context.WithoutCancel(ctx), stream, group, msg.ID); err != nil {
		slog.Error("could not acknowledge a malformed delivery", "message_id", msg.ID, "error", err)
	}
}

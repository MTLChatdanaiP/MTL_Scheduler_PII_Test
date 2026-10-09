package worker

// RFC-003 §5 Delivery Envelope, §14 PII Considerations, exercised against a real Redis.
//
// These tests use their OWN consumer group and only read, process and delete their OWN
// messages, so they are safe to run against a Redis that also holds real work.

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/redis/go-redis/v9"

	"MTL_Scheduler_PII_Test/internal/cache"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

// privateGroup creates a consumer group that starts at "$" (new messages only) and removes it
// afterwards, so the tests never touch the group the real workers use.
func privateGroup(t *testing.T) string {
	t.Helper()
	g := "TestG_" + ulid.Make().String()
	if err := cache.Client.XGroupCreateMkStream(context.Background(), cache.TaskStream, g, "$").Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cache.Client.XGroupDestroy(context.Background(), cache.TaskStream, g) })
	return g
}

// readMine delivers new messages to the private group and returns the one for which match
// is true, skipping any others that happen to arrive.
func readMine(t *testing.T, group string, match func(redis.XMessage) bool) redis.XMessage {
	t.Helper()
	res, err := cache.Client.XReadGroup(context.Background(), &redis.XReadGroupArgs{
		Group: group, Consumer: "test-consumer", Streams: []string{cache.TaskStream, ">"}, Count: 500, Block: 2 * time.Second,
	}).Result()
	if err != nil {
		t.Fatalf("could not read the message back: %v", err)
	}
	for _, s := range res {
		for _, m := range s.Messages {
			if match(m) {
				return m
			}
		}
	}
	t.Fatal("the message was not delivered to the private group")
	return redis.XMessage{}
}

func hasJob(jobID string) func(redis.XMessage) bool {
	return func(m redis.XMessage) bool { v, _ := m.Values["job_id"].(string); return v == jobID }
}

// streamEntriesFor returns the most recent stream entries for one job id.
func streamEntriesFor(t *testing.T, jobID string) []redis.XMessage {
	t.Helper()
	all, err := cache.Client.XRevRangeN(context.Background(), cache.TaskStream, "+", "-", 500).Result()
	if err != nil {
		t.Fatal(err)
	}
	var mine []redis.XMessage
	for _, m := range all {
		if hasJob(jobID)(m) {
			mine = append(mine, m)
		}
	}
	return mine
}

// forgetEntries deletes a test's own stream entries at the end of the test.
func forgetEntries(t *testing.T, jobIDs ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, j := range jobIDs {
			for _, m := range streamEntriesFor(t, j) {
				cache.Client.XDel(context.Background(), cache.TaskStream, m.ID)
			}
		}
	})
}

// captureOutput runs fn and returns everything it wrote to stdout and to the standard logger
// (which slog's default handler uses).
func captureOutput(t *testing.T, fn func()) string {
	t.Helper()

	oldOut := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	var logged bytes.Buffer
	log.SetOutput(&logged)

	copied := make(chan string)
	go func() {
		var b bytes.Buffer
		io.Copy(&b, r)
		copied <- b.String()
	}()

	fn()

	w.Close()
	os.Stdout = oldOut
	log.SetOutput(os.Stderr)

	return <-copied + logged.String()
}

func entryExists(t *testing.T, id string) bool {
	t.Helper()
	got, err := cache.Client.XRange(context.Background(), cache.TaskStream, id, id).Result()
	if err != nil {
		t.Fatal(err)
	}
	return len(got) > 0
}

func eventCount(t *testing.T, jobID string, eventType string) int64 {
	t.Helper()
	var n int64
	q := database.DB.Model(&models.EventEnvelope{}).Where("event_type = ?", eventType)
	if jobID != "" {
		q = q.Where("job_id = ?", jobID)
	}
	q.Count(&n)
	return n
}

// ---------------------------------------------------------------------------
// Safe logging (RFC-003 §14)
// ---------------------------------------------------------------------------

func TestDescribeMessage_ShowsOnlyTheIds(t *testing.T) {
	msg := redis.XMessage{ID: "1700000000000-0", Values: map[string]interface{}{
		"job_id": "J1", "trace_id": "T1",
		"payload": "secret 555-123-4567", "task_name": "jane.doe@example.com", "anything": "else",
	}}

	got := describeMessage(msg)

	if got != "id=1700000000000-0 job_id=J1 trace_id=T1" {
		t.Fatalf("unexpected description: %q", got)
	}
	for _, leaked := range []string{"secret", "555", "jane", "else", "payload"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("the description leaked %q: %s", leaked, got)
		}
	}
}

func TestDescribeMessage_HandlesAbsentAndUnsafeIds(t *testing.T) {
	absent := describeMessage(redis.XMessage{ID: "1-0", Values: map[string]interface{}{"foo": "bar"}})
	if absent != "id=1-0 job_id=- trace_id=-" {
		t.Fatalf("absent ids should print as -, got %q", absent)
	}

	unsafe := describeMessage(redis.XMessage{ID: "1-0", Values: map[string]interface{}{"job_id": "jane@example.com", "trace_id": "line\nbreak"}})
	if strings.Contains(unsafe, "jane") || strings.Contains(unsafe, "\n") {
		t.Fatalf("an unsafe id must never be printed, got %q", unsafe)
	}
}

// ---------------------------------------------------------------------------
// The envelope on the wire, and delete-after-ack (RFC-003 §5, §14)
// ---------------------------------------------------------------------------

func TestPublishEnvelope_WhatReachesRedisIsExactlyTheAllowedFields(t *testing.T) {
	task := newRunnableTask(t, "fail_permanent", "export for jane.doe@example.com", `{"note":"call 555-123-4567"}`)
	forgetEntries(t, task.JobId)
	group := privateGroup(t)

	env := cache.DeliveryEnvelope{JobID: task.JobId, CorrelationID: task.ExecutionChainId, TraceID: "trace-wire-1"}
	if err := PublishEnvelope(context.Background(), env); err != nil {
		t.Fatal(err)
	}

	msg := readMine(t, group, hasJob(task.JobId))

	allowed := map[string]bool{}
	for _, k := range cache.AllowedEnvelopeKeys {
		allowed[k] = true
	}
	for k, v := range msg.Values {
		if !allowed[k] {
			t.Errorf("Redis holds a key that is not allowed: %q", k)
		}
		// nothing the caller wrote may appear in any value
		s, _ := v.(string)
		for _, leaked := range []string{"jane", "555", "export", "fail_permanent", "note"} {
			if strings.Contains(s, leaked) {
				t.Errorf("value of %q contains caller text %q: %q", k, leaked, s)
			}
		}
	}
	if msg.Values["trace_id"] != "trace-wire-1" || msg.Values["correlation_id"] != task.ExecutionChainId || msg.Values["schema_version"] != "1" {
		t.Fatalf("envelope fields are wrong on the wire: %v", msg.Values)
	}
}

func TestPublishEnvelope_RefusesAnEnvelopeWithNoUsableJobID(t *testing.T) {
	for _, id := range []string{"", "has space", "jane@example.com"} {
		if err := PublishEnvelope(context.Background(), cache.DeliveryEnvelope{JobID: id}); err == nil {
			t.Errorf("job id %q should not be publishable", id)
		}
	}
}

func TestPublishToStream_KeepsWorkingWithJustAJobID(t *testing.T) {
	jobID := "compat-" + ulid.Make().String()
	forgetEntries(t, jobID)

	PublishToStream(context.Background(), jobID)

	entries := streamEntriesFor(t, jobID)
	if len(entries) != 1 || entries[0].Values["job_id"] != jobID {
		t.Fatalf("the old entry point should still publish, got %v", entries)
	}
}

func TestProcessStream_AnAcknowledgedMessageIsDeletedFromTheStream(t *testing.T) {
	task := newRunnableTask(t, "fail_permanent", "n", `{}`)
	other := "other-" + ulid.Make().String() // a second message that must NOT be touched
	forgetEntries(t, task.JobId, other)
	group := privateGroup(t)

	PublishEnvelope(context.Background(), cache.DeliveryEnvelope{JobID: task.JobId, CorrelationID: task.ExecutionChainId})
	PublishEnvelope(context.Background(), cache.DeliveryEnvelope{JobID: other})

	msg := readMine(t, group, hasJob(task.JobId))
	if !entryExists(t, msg.ID) {
		t.Fatal("the message should exist before it is processed")
	}

	ProcessStream(context.Background(), "test-consumer", cache.TaskStream, group, msg)

	if entryExists(t, msg.ID) {
		t.Fatal("an acknowledged message must be deleted, so Redis holds only work in flight")
	}
	pending, err := cache.Client.XPending(context.Background(), cache.TaskStream, group).Result()
	if err != nil {
		t.Fatal(err)
	}
	// the "other" message was delivered to this group's read but never processed, so it may still be pending
	for _, e := range streamEntriesFor(t, other) {
		if !entryExists(t, e.ID) {
			t.Fatal("deleting one message must not delete another")
		}
	}
	_ = pending

	var got models.Task
	database.DB.Where("job_id = ?", task.JobId).First(&got)
	if got.Status != "Failed" {
		t.Fatalf("the task should have been processed (fail_permanent -> Failed), status = %s", got.Status)
	}
}

func TestProcessStream_APendingCountReturnsToZeroOnceProcessed(t *testing.T) {
	task := newRunnableTask(t, "fail_permanent", "n", `{}`)
	forgetEntries(t, task.JobId)
	group := privateGroup(t)

	PublishEnvelope(context.Background(), cache.DeliveryEnvelope{JobID: task.JobId})
	msg := readMine(t, group, hasJob(task.JobId))

	before, _ := cache.Client.XPending(context.Background(), cache.TaskStream, group).Result()
	ProcessStream(context.Background(), "test-consumer", cache.TaskStream, group, msg)
	after, _ := cache.Client.XPending(context.Background(), cache.TaskStream, group).Result()

	if after.Count != before.Count-1 {
		t.Fatalf("processing should remove exactly this message from pending: before %d, after %d", before.Count, after.Count)
	}
}

func TestAckAndDelete_DoesNotDeleteWhenTheAckDidNotAcknowledgeIt(t *testing.T) {
	jobID := "keep-" + ulid.Make().String()
	forgetEntries(t, jobID)
	PublishEnvelope(context.Background(), cache.DeliveryEnvelope{JobID: jobID})
	entries := streamEntriesFor(t, jobID)
	if len(entries) != 1 {
		t.Fatalf("setup failed, expected one entry, got %d", len(entries))
	}

	// a group that does not exist acknowledges nothing, which is the "someone else already
	// acked it" case: this caller must leave the entry for that other party to remove
	acked, err := ackAndDelete(context.Background(), cache.TaskStream, "NoSuchGroup_"+ulid.Make().String(), entries[0].ID)

	if acked {
		t.Fatal("nothing was acknowledged, so acked must be false")
	}
	if err == nil && !entryExists(t, entries[0].ID) {
		t.Fatal("an entry that this call did not acknowledge must not be deleted")
	}
	if !entryExists(t, entries[0].ID) {
		t.Fatal("the entry was deleted although the ack did not succeed")
	}
}

func TestProcessStream_AnOldStyleMessageWithOnlyAJobIDStillWorks(t *testing.T) {
	task := newRunnableTask(t, "fail_permanent", "n", `{}`)
	forgetEntries(t, task.JobId)
	group := privateGroup(t)

	// exactly what the publisher wrote before the envelope existed
	if err := cache.Client.XAdd(context.Background(), &redis.XAddArgs{Stream: cache.TaskStream, Values: map[string]interface{}{"job_id": task.JobId}}).Err(); err != nil {
		t.Fatal(err)
	}
	msg := readMine(t, group, hasJob(task.JobId))

	ProcessStream(context.Background(), "test-consumer", cache.TaskStream, group, msg)

	var got models.Task
	database.DB.Where("job_id = ?", task.JobId).First(&got)
	if got.Status != "Failed" {
		t.Fatalf("a message in the old format must still be processed, status = %s", got.Status)
	}
	if entryExists(t, msg.ID) {
		t.Fatal("and acknowledged and deleted")
	}
}

func TestProcessStream_AMalformedMessageIsDroppedAndRecorded(t *testing.T) {
	marker := "marker-" + ulid.Make().String()
	group := privateGroup(t)
	before := eventCount(t, "", "delivery.malformed")

	// no job_id at all, and a value that looks like personal data
	id, err := cache.Client.XAdd(context.Background(), &redis.XAddArgs{Stream: cache.TaskStream, Values: map[string]interface{}{"marker": marker, "note": "jane.doe@example.com"}}).Result()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cache.Client.XDel(context.Background(), cache.TaskStream, id) })

	msg := readMine(t, group, func(m redis.XMessage) bool { v, _ := m.Values["marker"].(string); return v == marker })

	out := captureOutput(t, func() {
		ProcessStream(context.Background(), "test-consumer", cache.TaskStream, group, msg)
	})

	if strings.Contains(out, "jane.doe") || strings.Contains(out, marker) {
		t.Fatalf("a malformed message's contents were written to the logs: %s", out)
	}
	if entryExists(t, msg.ID) {
		t.Fatal("a malformed message carries no work and must be removed, not left pending forever")
	}
	if got := eventCount(t, "", "delivery.malformed"); got != before+1 {
		t.Fatalf("expected one delivery.malformed event, before %d after %d", before, got)
	}
}

// ---------------------------------------------------------------------------
// Trace id (RFC-003 §5)
// ---------------------------------------------------------------------------

func TestRetryChildInheritsItsParentsTraceID(t *testing.T) {
	useTestPolicy(t, artifactPolicy())

	task := newRunnableTask(t, "fail_retryable", "n", `{}`)
	if err := database.DB.Model(&models.Task{}).Where("job_id = ?", task.JobId).UpdateColumn("trace_id", "trace-chain-7").Error; err != nil {
		t.Fatal(err)
	}

	ProcessTask(context.Background(), task.JobId, "trace-test-worker")

	var child models.Task
	if err := database.DB.Where("parent_run_id = ?", task.JobId).First(&child).Error; err != nil {
		t.Fatalf("no retry child was created: %v", err)
	}
	if child.TraceID != "trace-chain-7" {
		t.Fatalf("the whole chain should share one trace, child has %q", child.TraceID)
	}
}

// The old ProcessStream printed msg.Values for every delivery. This runs the real function over a
// message carrying extra fields and checks what actually came out.
func TestProcessStream_NeverPrintsTheWholeMessage(t *testing.T) {
	task := newRunnableTask(t, "fail_permanent", "n", `{}`)
	forgetEntries(t, task.JobId)
	group := privateGroup(t)

	err := cache.Client.XAdd(context.Background(), &redis.XAddArgs{Stream: cache.TaskStream, Values: map[string]interface{}{
		"job_id": task.JobId, "trace_id": "t-print-1", "note": "jane.doe@example.com secret-xyz",
	}}).Err()
	if err != nil {
		t.Fatal(err)
	}
	msg := readMine(t, group, hasJob(task.JobId))

	out := captureOutput(t, func() {
		ProcessStream(context.Background(), "test-consumer", cache.TaskStream, group, msg)
	})

	if !strings.Contains(out, task.JobId) {
		t.Fatalf("the job id should still be logged so a delivery can be followed, got: %s", out)
	}
	for _, leaked := range []string{"jane.doe", "secret-xyz", "note"} {
		if strings.Contains(out, leaked) {
			t.Fatalf("ProcessStream wrote %q from the message to the logs: %s", leaked, out)
		}
	}
}

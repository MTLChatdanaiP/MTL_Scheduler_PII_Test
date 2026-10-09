package unit_test

// RFC-003 §5 Delivery Envelope, §14 PII Considerations.

import (
	"MTL_Scheduler_PII_Test/internal/cache"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestValidTraceID(t *testing.T) {
	good := []string{"01HZX3K5Y6ABCDEFGHJKMNPQRS", "trace-1", "a.b_c:d-e", "A", strings.Repeat("a", 64)}
	bad := []string{"", "a@b.com", "has space", "line\nbreak", "tab\t", "semi;colon", "slash/x", "ไทย", strings.Repeat("a", 65), "<script>"}

	for _, s := range good {
		if !cache.ValidTraceID(s) {
			t.Errorf("%q should be a valid id", s)
		}
	}
	for _, s := range bad {
		if cache.ValidTraceID(s) {
			t.Errorf("%q must NOT be a valid id", s)
		}
	}
}

func TestNewTraceID_IsValidAndUnique(t *testing.T) {
	a, b := cache.NewTraceID(), cache.NewTraceID()
	if !cache.ValidTraceID(a) || a == b {
		t.Fatalf("expected two distinct valid ids, got %q and %q", a, b)
	}
}

func TestEnvelope_RoundTrip(t *testing.T) {
	created := time.Date(2026, 10, 6, 12, 0, 0, 123456789, time.UTC)
	in := cache.DeliveryEnvelope{JobID: "job1", CorrelationID: "chain1", TraceID: "trace1", CreatedAt: created}

	out, err := cache.ParseEnvelope(in.Values())
	if err != nil {
		t.Fatal(err)
	}

	if out.JobID != "job1" || out.CorrelationID != "chain1" || out.TraceID != "trace1" || out.SchemaVersion != cache.EnvelopeSchemaVersion || !out.CreatedAt.Equal(created) {
		t.Fatalf("round trip lost something: %+v", out)
	}
}

func TestEnvelope_AMessageWrittenBeforeTheEnvelopeExistedStillParses(t *testing.T) {
	// the stream may hold messages that are only {job_id}
	out, err := cache.ParseEnvelope(map[string]interface{}{"job_id": "01OLDMESSAGE"})
	if err != nil || out.JobID != "01OLDMESSAGE" {
		t.Fatalf("an old message must still be deliverable: %+v, %v", out, err)
	}
	if out.TraceID != "" || out.CorrelationID != "" {
		t.Fatalf("absent optional fields should stay empty, got %+v", out)
	}
}

func TestEnvelope_UnknownKeysAreIgnored(t *testing.T) {
	out, err := cache.ParseEnvelope(map[string]interface{}{"job_id": "j1", "payload": "secret", "future_field": "x"})
	if err != nil || out.JobID != "j1" {
		t.Fatalf("unknown keys must not break delivery: %+v, %v", out, err)
	}
}

func TestEnvelope_MalformedMessagesAreRejected(t *testing.T) {
	bad := map[string]map[string]interface{}{
		"empty message":       {},
		"no job_id":           {"trace_id": "t"},
		"empty job_id":        {"job_id": ""},
		"non-string job_id":   {"job_id": 42},
		"job_id with space":   {"job_id": "a b"},
		"job_id with email":   {"job_id": "jane@example.com"},
		"job_id with newline": {"job_id": "a\nb"},
		"job_id too long":     {"job_id": strings.Repeat("x", 65)},
	}
	for name, values := range bad {
		if _, err := cache.ParseEnvelope(values); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
}

func TestEnvelope_AnErrorNeverEchoesTheMessage(t *testing.T) {
	// the rejected value is exactly the kind of thing that may be PII
	_, err := cache.ParseEnvelope(map[string]interface{}{"job_id": "jane.doe@example.com 555-123-4567"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "jane") || strings.Contains(err.Error(), "555") {
		t.Fatalf("the error leaked the message content: %q", err.Error())
	}
}

func TestEnvelope_BadOptionalFieldsAreIgnoredNotFatal(t *testing.T) {
	out, err := cache.ParseEnvelope(map[string]interface{}{
		"job_id": "j1", "trace_id": "has space", "correlation_id": 7, "created_at": "yesterday", "schema_version": "way-too-long-version",
	})
	if err != nil {
		t.Fatalf("a bad optional field must not make a valid job undeliverable: %v", err)
	}
	if out.TraceID != "" || out.CorrelationID != "" || !out.CreatedAt.IsZero() || out.SchemaVersion != "" {
		t.Fatalf("bad optional fields should be dropped, got %+v", out)
	}
}

func TestEnvelope_UnsafeOptionalIdsAreLeftOutOfWhatIsPublished(t *testing.T) {
	v := cache.DeliveryEnvelope{JobID: "j1", TraceID: "jane@example.com", CorrelationID: "line\nbreak"}.Values()
	if _, ok := v["trace_id"]; ok {
		t.Fatal("an unsafe trace id must not be written to Redis")
	}
	if _, ok := v["correlation_id"]; ok {
		t.Fatal("an unsafe correlation id must not be written to Redis")
	}
}

func TestEnvelope_Validate(t *testing.T) {
	if err := (cache.DeliveryEnvelope{JobID: "ok"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (cache.DeliveryEnvelope{JobID: ""}).Validate(); err == nil {
		t.Fatal("an envelope with no job id must not validate")
	}
}

// ---------------------------------------------------------------------------
// The PII guard. Redis must never hold caller text, so the set of keys is fixed. If somebody
// adds a field to DeliveryEnvelope, these fail until they have decided whether it is safe.
// ---------------------------------------------------------------------------

func TestEnvelope_OnlyTheAllowedKeysCanBeWritten(t *testing.T) {
	full := cache.DeliveryEnvelope{JobID: "j", CorrelationID: "c", TraceID: "t", CreatedAt: time.Now(), SchemaVersion: "1"}

	allowed := map[string]bool{}
	for _, k := range cache.AllowedEnvelopeKeys {
		allowed[k] = true
	}
	for k := range full.Values() {
		if !allowed[k] {
			t.Errorf("key %q is written to Redis but is not in AllowedEnvelopeKeys", k)
		}
	}
}

func TestEnvelope_HasExactlyTheFieldsTheAllowListCovers(t *testing.T) {
	if got := reflect.TypeOf(cache.DeliveryEnvelope{}).NumField(); got != len(cache.AllowedEnvelopeKeys) {
		t.Fatalf("DeliveryEnvelope has %d fields but AllowedEnvelopeKeys has %d: a new field must be added to the allow list deliberately, after deciding it cannot carry caller text", got, len(cache.AllowedEnvelopeKeys))
	}
}

func TestPayloadModeIsReference(t *testing.T) {
	if cache.PayloadMode != "REFERENCE" {
		t.Fatalf("PayloadMode = %q: embedding a payload in Redis is a PII decision, not a tweak", cache.PayloadMode)
	}
	for _, k := range cache.AllowedEnvelopeKeys {
		if strings.Contains(strings.ToLower(k), "payload") || strings.Contains(strings.ToLower(k), "name") || strings.Contains(strings.ToLower(k), "type") {
			t.Errorf("allowed key %q looks like it could carry payload, task name or task type", k)
		}
	}
}

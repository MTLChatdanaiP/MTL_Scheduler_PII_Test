package cache

import (
	"errors"
	"regexp"
	"time"

	"github.com/oklog/ulid/v2"
)

// RFC-003 §5 Delivery Envelope.
//
// PayloadMode is the decision the RFC says "must be decided with PII and payload-size
// considerations": the payload is REFERENCED, never embedded. A Redis message carries
// the id of the run, and the worker reads the payload from Postgres, where the PII
// policy has already redacted it. Redis therefore never holds payload text.
const PayloadMode = "REFERENCE"

// EnvelopeSchemaVersion lets the shape change later without guessing what an old
// message in the stream looks like.
const EnvelopeSchemaVersion = "1"

const (
	keyJobID         = "job_id" // RFC run_id. The key name is kept: the reader already running uses it.
	keyCorrelationID = "correlation_id"
	keyTraceID       = "trace_id"
	keyCreatedAt     = "created_at"
	keySchemaVersion = "schema_version"
)

// AllowedEnvelopeKeys is every key a delivery message may contain, and nothing else.
// There is deliberately no payload, task name or task type: those are caller-supplied
// free text, and copying them into Redis would copy caller text into Redis.
var AllowedEnvelopeKeys = []string{keyJobID, keyCorrelationID, keyTraceID, keyCreatedAt, keySchemaVersion}

// safeID is what an id may look like: short, and made only of characters that cannot
// carry free text or break a log line. ULIDs, which this system generates, match.
var safeID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

var safeVersion = regexp.MustCompile(`^[0-9A-Za-z.]{1,8}$`)

// ValidTraceID takes a string and returns true only if it is a safe id.
func ValidTraceID(s string) bool { return safeID.MatchString(s) }

// NewTraceID returns a fresh trace id.
func NewTraceID() string { return ulid.Make().String() }

// errNoJobID is deliberately generic. An error here must never echo the message it was
// about, because that message is the thing that may contain something it should not.
var errNoJobID = errors.New("delivery has no usable job_id")

// DeliveryEnvelope is what goes into Redis for one run.
type DeliveryEnvelope struct {
	JobID         string // the run id, and also the payload reference
	CorrelationID string // the execution chain id: the same for every retry of one job
	TraceID       string
	CreatedAt     time.Time
	SchemaVersion string
}

// Validate returns an error unless the envelope can be published.
func (e DeliveryEnvelope) Validate() error {
	if !safeID.MatchString(e.JobID) {
		return errNoJobID
	}
	return nil
}

// Values takes the envelope and returns the map XAdd needs, by writing each field as a
// string. Empty fields, and optional ids that are not safe, are left out rather than
// blocking delivery.
func (e DeliveryEnvelope) Values() map[string]interface{} {
	v := map[string]interface{}{keyJobID: e.JobID}

	if safeID.MatchString(e.CorrelationID) {
		v[keyCorrelationID] = e.CorrelationID
	}
	if safeID.MatchString(e.TraceID) {
		v[keyTraceID] = e.TraceID
	}
	if !e.CreatedAt.IsZero() {
		v[keyCreatedAt] = e.CreatedAt.UTC().Format(time.RFC3339Nano)
	}

	version := e.SchemaVersion
	if version == "" {
		version = EnvelopeSchemaVersion
	}
	v[keySchemaVersion] = version

	return v
}

// ParseEnvelope takes msg.Values and returns the envelope, or an error if the message
// has no usable job_id.
//
// It accepts messages written before this existed (job_id only), ignores keys it does
// not know, and ignores optional fields of the wrong type or shape instead of rejecting
// a message that still carries valid work.
func ParseEnvelope(values map[string]interface{}) (DeliveryEnvelope, error) {
	jobID, ok := values[keyJobID].(string)
	if !ok || !safeID.MatchString(jobID) {
		return DeliveryEnvelope{}, errNoJobID
	}

	env := DeliveryEnvelope{JobID: jobID}

	if s, ok := values[keyCorrelationID].(string); ok && safeID.MatchString(s) {
		env.CorrelationID = s
	}
	if s, ok := values[keyTraceID].(string); ok && safeID.MatchString(s) {
		env.TraceID = s
	}
	if s, ok := values[keyCreatedAt].(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			env.CreatedAt = t
		}
	}
	if s, ok := values[keySchemaVersion].(string); ok && safeVersion.MatchString(s) {
		env.SchemaVersion = s
	}

	return env, nil
}

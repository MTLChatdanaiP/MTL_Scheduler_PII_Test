package alerts

// RFC-007 §12 Notification Adapters, §13 PII Safety.
//
// In-package and database-free: everything here runs against a local
// httptest server, so these are fast and can be run on every save.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"MTL_Scheduler_PII_Test/internals/models"
)

func testAlert() models.Alert {
	return models.Alert{
		AlertID:     "01TEST000000000000000000",
		AlertType:   "WORKER_OFFLINE",
		Severity:    "CRITICAL",
		Status:      "OPEN",
		Summary:     "WORKER_OFFLINE on WORKER Consumer-a",
		SubjectType: "WORKER",
		SubjectID:   "Consumer-a",
		RuleID:      "worker-offline-default",
		RuleVersion: 1,
		Evidence:    `{"age_seconds":91.4,"threshold":60}`,
	}
}

func TestWebhookAdapter_SendSucceedsOn2xx(t *testing.T) {
	var receivedBody []byte
	var receivedContentType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		receivedContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	adapter := NewWebhookAdapter(server.URL)

	if err := adapter.Send(context.Background(), testAlert()); err != nil {
		t.Fatalf("Send returned an error on a 200 response: %v", err)
	}

	if receivedContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", receivedContentType)
	}

	// The body must be valid JSON on the wire, not just marshalable in theory.
	var decoded map[string]interface{}
	if err := json.Unmarshal(receivedBody, &decoded); err != nil {
		t.Fatalf("server received invalid JSON: %v\ngot: %s", err, receivedBody)
	}
}

func TestWebhookAdapter_SendFailsOnNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("upstream exploded"))
	}))
	defer server.Close()

	adapter := NewWebhookAdapter(server.URL)

	err := adapter.Send(context.Background(), testAlert())
	if err == nil {
		t.Fatal("Send returned nil on a 500 response")
	}

	// The status code has to be in the message: LastError is the only record of
	// why a notification failed, and "it failed" without a code is not
	// actionable.
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, expected it to contain the status code", err.Error())
	}
	if !strings.Contains(err.Error(), "upstream exploded") {
		t.Errorf("error = %q, expected it to contain the response body", err.Error())
	}
}

func TestWebhookAdapter_TruncatesLongErrorBody(t *testing.T) {
	// A remote endpoint can echo the request back in its error body. Storing
	// that verbatim would reintroduce whatever was sent, and would put an
	// unbounded string into LastError.
	huge := strings.Repeat("x", 5000)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(huge))
	}))
	defer server.Close()

	err := NewWebhookAdapter(server.URL).Send(context.Background(), testAlert())
	if err == nil {
		t.Fatal("Send returned nil on a 400 response")
	}

	// Message length is the truncated body plus the wrapper text, so allow
	// slack for the wrapper rather than asserting an exact figure.
	if len(err.Error()) > maxLastErrorLength+100 {
		t.Errorf("error message is %d chars, expected the body to be truncated to %d", len(err.Error()), maxLastErrorLength)
	}
}

// THE TEST THAT PROTECTS THE SWEEP.
//
// The notification pass runs on the sweep goroutine. http.DefaultClient has no
// timeout, so if this adapter ever loses its explicit Client.Timeout, one
// unresponsive endpoint blocks the sweep forever and every alert behind it
// stops being processed.
func TestWebhookAdapter_TimesOutRatherThanHanging(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(webhookTimeout + 2*time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	adapter := NewWebhookAdapter(server.URL)

	start := time.Now()
	err := adapter.Send(context.Background(), testAlert())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Send returned nil against an endpoint that never responded in time")
	}

	if elapsed > webhookTimeout+2*time.Second {
		t.Errorf("Send took %v -- it waited for the handler instead of timing out at %v", elapsed, webhookTimeout)
	}
}

func TestWebhookAdapter_ContextCancellationAborts(t *testing.T) {
	// Shutdown cancels the sweep's context. An in-flight request must abort
	// rather than hold the graceful-shutdown phase open for the full timeout.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := NewWebhookAdapter(server.URL).Send(ctx, testAlert())

	if err == nil {
		t.Fatal("Send returned nil despite the context being cancelled")
	}
	if time.Since(start) > 2*time.Second {
		t.Error("Send ignored context cancellation")
	}
}

// ---------------------------------------------------------------------------
// buildWebhookBody — RFC-007 §13 PII Safety
// ---------------------------------------------------------------------------

func TestBuildWebhookBody_IncludesExpectedFields(t *testing.T) {
	body, err := buildWebhookBody(testAlert())
	if err != nil {
		t.Fatalf("buildWebhookBody failed: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}

	for _, field := range []string{
		"alert_id", "alert_type", "severity", "status",
		"summary", "subject_type", "subject_id", "evidence",
	} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("body is missing %q", field)
		}
	}

	// Evidence is decoded into a real object rather than shipped as an escaped
	// JSON string. A recipient should not have to parse a string that is itself
	// JSON.
	if _, ok := decoded["evidence"].(map[string]interface{}); !ok {
		t.Errorf("evidence = %T, want a nested object", decoded["evidence"])
	}
}

// THE EXCLUSION TEST. This is the one that catches someone later "helpfully"
// marshalling models.Alert directly.
//
// §13 PII Safety is about what leaves the system. Alert content is safe
// in-system by construction, but a webhook sends it to a destination this
// codebase does not control. Marshalling the struct would ship gorm.Model's
// internals, and would mean any field added to Alert in future leaves the
// system without anyone deciding it should.
func TestBuildWebhookBody_ExcludesInternalFields(t *testing.T) {
	body, err := buildWebhookBody(testAlert())
	if err != nil {
		t.Fatalf("buildWebhookBody failed: %v", err)
	}

	var decoded map[string]interface{}
	json.Unmarshal(body, &decoded)

	for _, field := range []string{
		"ID", "CreatedAt", "UpdatedAt", "DeletedAt", // gorm.Model internals
		"AcknowledgedBy",     // who responded is internal, not the recipient's business
		"SourceAnnotationID", // an internal correlation key, meaningless outside
	} {
		if _, present := decoded[field]; present {
			t.Errorf("body contains %q -- it is marshalling the Alert struct instead of an explicit payload", field)
		}
	}
}

func TestBuildWebhookBody_SurvivesUnparseableEvidence(t *testing.T) {
	// Evidence is assembled from a metric sample and should always be valid
	// JSON, but a malformed blob must not stop delivery: everything actually
	// useful to a recipient (type, severity, subject, summary) is in the payload
	// regardless. Failing here would burn all three retry attempts on evidence
	// that will never parse.
	alert := testAlert()
	alert.Evidence = "{not valid json"

	body, err := buildWebhookBody(alert)
	if err != nil {
		t.Fatalf("buildWebhookBody failed on malformed evidence: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}

	if decoded["alert_type"] != "WORKER_OFFLINE" {
		t.Error("the rest of the payload was lost along with the bad evidence")
	}
	if _, present := decoded["evidence"]; present {
		t.Error("evidence is present despite being unparseable -- omitempty should drop it")
	}
}

func TestShouldNotify(t *testing.T) {
	tests := []struct {
		severity string
		want     bool
	}{
		{"CRITICAL", true},
		{"WARNING", true},
		// An INFO alert paging someone is how people learn to ignore alerts.
		{"INFO", false},
		{"", false},
		{"critical", false}, // exact match only, no normalisation
	}

	for _, tt := range tests {
		t.Run(tt.severity, func(t *testing.T) {
			if got := shouldNotify(tt.severity); got != tt.want {
				t.Errorf("shouldNotify(%q) = %v, want %v", tt.severity, got, tt.want)
			}
		})
	}
}

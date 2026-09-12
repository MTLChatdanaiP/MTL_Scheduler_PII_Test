package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"
)

const baseURL = "http://localhost:8080"

// apiKey returns the key the e2e suite authenticates with.
//
// Read from the environment rather than hardcoded so CI and a local machine can
// use different keys: CI writes its own internals/auth/config.json with a
// throwaway key, while locally you have whatever is in your real config file.
// The fallback is the dev key so `go test ./test/e2e/...` works with no setup.
func apiKey() string {
	if key := os.Getenv("E2E_API_KEY"); key != "" {
		return key
	}
	return "dev-local-key-changeme"
}

// doRequest issues a request with the API key attached.
//
// The key goes on EVERY request, not only the ones currently behind a scope.
// Ungated routes ignore an unknown header, so the cost is nothing, and it means
// gating a new route later does not silently break an e2e test that was written
// before the gate existed -- which is exactly what happened to
// TestCreateTask_RedactsPII when /pii/:job_id became scoped.
func doRequest(t *testing.T, method, path string, body []byte) *http.Response {
	t.Helper()

	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, baseURL+path, reader)
	if err != nil {
		t.Fatalf("failed to build %s %s: %v", method, path, err)
	}

	req.Header.Set("X-API-Key", apiKey())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s failed: %v", method, path, err)
	}

	// A 403 here means the key is unknown or the principal lacks the scope, not
	// that the endpoint is broken. Saying so explicitly saves the next person
	// debugging a "nil map" failure three assertions further down.
	if resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		t.Fatalf("%s %s returned 403 -- check E2E_API_KEY and that the principal has the scope for this route", method, path)
	}

	return resp
}

func postTask(t *testing.T, taskName, taskType, payload string) map[string]interface{} {
	t.Helper()

	body, _ := json.Marshal(map[string]string{
		"TaskName": taskName,
		"TaskType": taskType,
		"Payload":  payload,
	})

	resp := doRequest(t, http.MethodPost, "/tasks", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	var created map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return created
}

func getJSON(t *testing.T, path string) map[string]interface{} {
	t.Helper()

	resp := doRequest(t, http.MethodGet, path, nil)
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return result
}

// getJSONArray exists because some endpoints return a top-level array rather
// than an object, and decoding an array into a map silently yields an empty map
// instead of an error.
func getJSONArray(t *testing.T, path string) []interface{} {
	t.Helper()

	resp := doRequest(t, http.MethodGet, path, nil)
	defer resp.Body.Close()

	var result []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode %s as an array: %v", path, err)
	}
	return result
}

func TestCreateTask_RedactsPII(t *testing.T) {
	created := postTask(t, "e2e_redact_test", "dummy", "email me at hello@example.com")

	jobId, _ := created["JobId"].(string)
	if jobId == "" {
		t.Fatal("expected a JobId in the response")
	}

	payload, _ := created["Payload"].(string)
	if payload == "email me at hello@example.com" {
		t.Error("expected payload to be redacted, was not")
	}

	// This route requires pii.findings.view since RFC-006 §33. Before the header
	// was added it returned 403, and the failure surfaced as "expected at least
	// 1 PII record" -- pointing at detection rather than at auth.
	records := getJSONArray(t, "/pii/"+jobId)

	if len(records) == 0 {
		t.Fatal("expected at least 1 PII record, got none")
	}
}

func TestFullTaskLifecycle_EventsInOrder(t *testing.T) {
	created := postTask(t, "e2e_lifecycle_test", "dummy", "no pii in this one")
	jobId, _ := created["JobId"].(string)

	deadline := time.Now().Add(150 * time.Second)
	var events []map[string]interface{}
	found := false

	for time.Now().Before(deadline) {
		resp := doRequest(t, http.MethodGet, "/events/"+jobId, nil)
		json.NewDecoder(resp.Body).Decode(&events)
		resp.Body.Close()

		for _, e := range events {
			if eventType, _ := e["event_type"].(string); eventType == "task.completed" {
				found = true
			}
		}

		if found {
			break
		}
		time.Sleep(2 * time.Second)
	}

	if !found {
		t.Fatalf("task never reached task.completed within the deadline, got %d events", len(events))
	}

	wantSequence := []string{"task.created", "task.queued", "task.started", "task.completed"}
	gotIndex := 0
	for _, e := range events {
		eventType, _ := e["event_type"].(string)
		if gotIndex < len(wantSequence) && eventType == wantSequence[gotIndex] {
			gotIndex++
		}
	}
	if gotIndex != len(wantSequence) {
		t.Errorf("expected to find milestone events %v in order, only matched %d of them", wantSequence, gotIndex)
	}
}

func TestRetryChain_ShowsTwoLinkedRuns(t *testing.T) {
	created := postTask(t, "e2e_retry_test", "fail_retryable", "will retry once")
	jobId, _ := created["JobId"].(string)

	time.Sleep(45 * time.Second) // retry backoff + second run's processing

	chain := getJSON(t, fmt.Sprintf("/runs/%s/chain", jobId))
	runs, ok := chain["runs"].([]interface{})
	if !ok || len(runs) != 2 {
		t.Fatalf("expected 2 runs in chain, got: %v", chain)
	}
}

func TestDebugReset_ClearsAllTables(t *testing.T) {
	postTask(t, "e2e_reset_test", "dummy", "will be wiped")

	resp := doRequest(t, http.MethodDelete, "/debug/reset", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	tasks := getJSONArray(t, "/tasks")

	if len(tasks) != 0 {
		t.Errorf("expected empty task list after reset, got %d tasks", len(tasks))
	}
}

func TestMetrics_ReflectsRealCounts(t *testing.T) {
	resp := doRequest(t, http.MethodDelete, "/debug/reset", nil)
	resp.Body.Close()

	postTask(t, "e2e_metrics_1", "dummy", "count me")
	postTask(t, "e2e_metrics_2", "dummy", "count me too")

	metrics := getJSON(t, "/metrics")

	created, _ := metrics["runs_created_total"].(float64)
	if created != 2 {
		t.Errorf("expected runs_created_total 2, got %v", created)
	}
}

// RFC-007 §6/§7: the alerts endpoint is reachable and correctly gated.
//
// Deliberately does NOT assert alerts exist -- that would depend on a monitoring
// annotation having fired, which needs a stuck task and a 60s threshold. The
// alerting lifecycle itself is covered properly in the integration tests, where
// an annotation can be seeded directly. What this checks is the thing only an
// e2e test can: that the route is wired, the scope is right, and the response
// has the shape the dashboard will later depend on.
func TestAlerts_EndpointIsReachableAndScoped(t *testing.T) {
	result := getJSON(t, "/alerts")

	if _, ok := result["count"]; !ok {
		t.Errorf("expected a count field in the /alerts response, got: %v", result)
	}
	if _, ok := result["alerts"]; !ok {
		t.Errorf("expected an alerts field in the /alerts response, got: %v", result)
	}
}

func TestAlerts_RejectsRequestWithoutKey(t *testing.T) {
	// Deliberately bypasses doRequest -- this is the one case that must NOT send
	// the header. Without this test, removing the auth middleware from the route
	// would break nothing visible.
	resp, err := http.Get(baseURL + "/alerts")
	if err != nil {
		t.Fatalf("GET /alerts failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for an unauthenticated request, got %d", resp.StatusCode)
	}
}

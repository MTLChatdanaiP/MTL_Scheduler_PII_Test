package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

const baseURL = "http://localhost:8080"

// apiKey returns the key the e2e suite authenticates with.
//
// Read from the environment rather than hardcoded so CI and a local machine can
// use different keys: CI writes its own internal/auth/config.json with a
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
// before the gate existed.
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

// getJSON fetches a path and decodes the JSON object it returns.
//
// It FAILS on any non-2xx status and prints the start of the body. It used to
// ignore both, so a route that had been removed (Gin answers "404 page not
// found" as plain text) showed up as an empty map or a baffling "cannot
// unmarshal number" error, several assertions away from the real cause.
func getJSON(t *testing.T, path string) map[string]interface{} {
	t.Helper()

	resp := doRequest(t, http.MethodGet, path, nil)
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s: failed to read body: %v", path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet := string(raw)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		t.Fatalf("GET %s returned %d: %s", path, resp.StatusCode, snippet)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("GET %s: response is not a JSON object: %v", path, err)
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

	// GET /pii/findings needs pii.findings.read (RFC-006 §33) and returns
	// {"piis": [...], "page": ...}. This replaced the old GET /pii/<jobId>.
	result := getJSON(t, "/pii/findings?run_id="+jobId+"&limit=50&offset=0")

	findings, _ := result["piis"].([]interface{})
	if len(findings) == 0 {
		t.Fatalf("expected at least 1 PII finding for run %s, got none (response: %v)", jobId, result)
	}
}

func TestFullTaskLifecycle_EventsInOrder(t *testing.T) {
	created := postTask(t, "e2e_lifecycle_test", "dummy", "no pii in this one")
	jobId, _ := created["JobId"].(string)

	// The run timeline (GET /runs/<id>/timeline, needs job.logs.read) replaced
	// the retired GET /events/<id>. It merges several sources, so only entries
	// with source EVENT are the lifecycle events this test is about.
	eventTypes := func(entries []interface{}) []string {
		var types []string
		for _, raw := range entries {
			e, _ := raw.(map[string]interface{})
			if e["source"] != "EVENT" {
				continue
			}
			if typ, _ := e["event_type"].(string); typ != "" {
				types = append(types, typ)
			}
		}
		return types
	}

	deadline := time.Now().Add(150 * time.Second)
	var types []string
	found := false

	for time.Now().Before(deadline) {
		result := getJSON(t, "/runs/"+jobId+"/timeline")
		entries, _ := result["entries"].([]interface{})
		types = eventTypes(entries)

		for _, typ := range types {
			if typ == "task.completed" {
				found = true
			}
		}

		if found {
			break
		}
		time.Sleep(2 * time.Second)
	}

	if !found {
		t.Fatalf("task never reached task.completed within the deadline, saw events: %v", types)
	}

	wantSequence := []string{"task.created", "task.queued", "task.started", "task.completed"}
	gotIndex := 0
	for _, typ := range types {
		if gotIndex < len(wantSequence) && typ == wantSequence[gotIndex] {
			gotIndex++
		}
	}
	if gotIndex != len(wantSequence) {
		t.Errorf("expected to find milestone events %v in order, only matched %d of them (saw %v)", wantSequence, gotIndex, types)
	}
}

func TestRetryChain_ShowsTwoLinkedRuns(t *testing.T) {
	created := postTask(t, "e2e_retry_test", "fail_retryable", "will retry once")
	jobId, _ := created["JobId"].(string)

	chainID, _ := created["ExecutionChainId"].(string)
	if chainID == "" {
		chainID = jobId // CreateTask_Direct sets the chain id to the first run's id
	}

	// GET /runs?execution_chain_id=... replaced the old GET /runs/<id>/chain.
	// Poll instead of a fixed 45s sleep: the retry appears after its backoff and
	// this returns as soon as it does.
	deadline := time.Now().Add(90 * time.Second)
	var runs []interface{}

	for time.Now().Before(deadline) {
		result := getJSON(t, "/runs?execution_chain_id="+chainID+"&limit=50&offset=0")
		runs, _ = result["runs"].([]interface{})
		if len(runs) >= 2 {
			break
		}
		time.Sleep(2 * time.Second)
	}

	if len(runs) != 2 {
		t.Fatalf("expected 2 runs in the chain, got %d", len(runs))
	}

	// "Linked" means more than "two rows share a chain id": the second run must
	// be retry 1 and point back at the first. (Task fields are PascalCase on the wire.)
	linked := false
	for _, raw := range runs {
		run, _ := raw.(map[string]interface{})
		retryIndex, _ := run["RetryIndex"].(float64)
		parent, _ := run["ParentRunId"].(string)
		if retryIndex == 1 && parent == jobId {
			linked = true
		}
	}
	if !linked {
		t.Errorf("expected a run with RetryIndex 1 and ParentRunId %s in the chain, got: %v", jobId, runs)
	}
}

func TestDebugReset_ClearsAllTables(t *testing.T) {
	postTask(t, "e2e_reset_test", "dummy", "will be wiped")

	resp := doRequest(t, http.MethodDelete, "/debug/reset", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// There is no GET /tasks (only POST); the run list is GET /runs.
	result := getJSON(t, "/runs?limit=50&offset=0")
	runs, _ := result["runs"].([]interface{})

	if len(runs) != 0 {
		t.Errorf("expected an empty run list after reset, got %d runs", len(runs))
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

// RFC-007 §6/§7 and RFC-008 §13: the alerts endpoint is reachable, correctly
// gated, and has the response shape the dashboard depends on:
// {"alerts": [...], "page": {"count", "limit", "has_more"}, "freshness", "live"}.
//
// Deliberately does NOT assert alerts exist -- that would depend on a monitoring
// annotation having fired, which needs a stuck task and a 60s threshold. The
// alerting lifecycle itself is covered in the integration tests, where an
// annotation can be seeded directly.
func TestAlerts_EndpointIsReachableAndScoped(t *testing.T) {
	result := getJSON(t, "/alerts")

	if _, ok := result["alerts"]; !ok {
		t.Errorf("expected an alerts field in the /alerts response, got: %v", result)
	}

	page, ok := result["page"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a page object in the /alerts response, got: %v", result)
	}
	if _, ok := page["count"]; !ok {
		t.Errorf("expected page.count in the /alerts response, got page: %v", page)
	}

	for _, key := range []string{"freshness", "live"} {
		if _, ok := result[key]; !ok {
			t.Errorf("expected a %s field in the /alerts response, got: %v", key, result)
		}
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

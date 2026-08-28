package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

const baseURL = "http://localhost:8080"

func postTask(t *testing.T, taskName, taskType, payload string) map[string]interface{} {
	body, _ := json.Marshal(map[string]string{
		"TaskName": taskName,
		"TaskType": taskType,
		"Payload":  payload,
	})

	resp, err := http.Post(baseURL+"/tasks", "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("POST /tasks failed: %v", err)
	}
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
	resp, err := http.Get(baseURL + path)
	if err != nil {
		t.Fatalf("GET %s failed: %v", path, err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
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

	resp, err := http.Get(baseURL + "/pii/" + jobId)
	if err != nil {
		t.Fatalf("GET /pii/%s failed: %v", jobId, err)
	}
	defer resp.Body.Close()

	var records []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&records); err != nil {
		t.Fatalf("failed to decode /pii response: %v", err)
	}

	if len(records) == 0 {
		t.Fatal("expected at least 1 PII record, got none")
	}
}

func TestFullTaskLifecycle_EventsInOrder(t *testing.T) {
	created := postTask(t, "e2e_lifecycle_test", "dummy", "no pii in this one")
	jobId, _ := created["JobId"].(string)

	deadline := time.Now().Add(90 * time.Second)
	var events []map[string]interface{}

	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/events/" + jobId)
		if err == nil {
			json.NewDecoder(resp.Body).Decode(&events)
			resp.Body.Close()
		}
		if len(events) >= 4 {
			break
		}
		time.Sleep(2 * time.Second)
	}

	if len(events) < 4 {
		t.Fatalf("expected at least 4 events, got %d", len(events))
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

	req, _ := http.NewRequest(http.MethodDelete, baseURL+"/debug/reset", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /debug/reset failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	resp2, err := http.Get(baseURL + "/tasks")
	if err != nil {
		t.Fatalf("GET /tasks failed: %v", err)
	}
	defer resp2.Body.Close()

	var tasks []interface{}
	json.NewDecoder(resp2.Body).Decode(&tasks)

	if len(tasks) != 0 {
		t.Errorf("expected empty task list after reset, got %d tasks", len(tasks))
	}
}

func TestMetrics_ReflectsRealCounts(t *testing.T) {
	req, _ := http.NewRequest(http.MethodDelete, baseURL+"/debug/reset", nil)
	http.DefaultClient.Do(req)

	postTask(t, "e2e_metrics_1", "dummy", "count me")
	postTask(t, "e2e_metrics_2", "dummy", "count me too")

	metrics := getJSON(t, "/metrics")

	created, _ := metrics["runs_created_total"].(float64)
	if created != 2 {
		t.Errorf("expected runs_created_total 2, got %v", created)
	}
}

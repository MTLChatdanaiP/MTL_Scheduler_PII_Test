package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

// Batch 7 (RFC-009 §25): the server says whether a queue is degraded, with the numbers; the dashboard only displays it.
func TestGetQueues_CarriesTheServersHealthVerdict(t *testing.T) {
	if err := database.DB.AutoMigrate(&models.QueueHealth{}); err != nil {
		t.Fatal(err)
	}
	quiet, backed := "b7-quiet-"+ulid.Make().String(), "b7-backed-"+ulid.Make().String()
	now := time.Now().UTC()
	database.DB.Create(&models.QueueHealth{QueueName: quiet, PendingCount: models.QueueDegradedPendingThreshold, SampledAt: now})
	database.DB.Create(&models.QueueHealth{QueueName: backed, PendingCount: models.QueueDegradedPendingThreshold + 1, SampledAt: now})
	t.Cleanup(func() { database.DB.Unscoped().Where("queue_name IN ?", []string{quiet, backed}).Delete(&models.QueueHealth{}) })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/queues", GetQueues)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/queues", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		Queues []map[string]interface{} `json:"queues"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	got := map[string]map[string]interface{}{}
	for _, q := range body.Queues {
		got[q["QueueName"].(string)] = q
	}
	if q := got[quiet]; q == nil || q["health"] != "HEALTHY" {
		t.Errorf("a queue exactly at the threshold is HEALTHY, got %v", q)
	}
	q := got[backed]
	if q == nil || q["health"] != "DEGRADED" {
		t.Fatalf("a queue above the threshold is DEGRADED, got %v", q)
	}
	if reason, _ := q["health_reason"].(string); reason == "" {
		t.Error("the verdict must carry its reason")
	}
	if _, ok := q["PendingCount"]; !ok {
		t.Error("the existing fields must still be there, unchanged")
	}
}

func TestSetRFCState_UsesTheNewestAttemptsCategoryAndTheRetryIndex(t *testing.T) {
	item := RunListItem{Task: models.Task{Status: "Failed", RetryIndex: 1}, Attempts: []models.Attempt{{FailureCategory: "APPLICATION_ERROR"}, {FailureCategory: "TIMEOUT"}}}
	item.setRFCState()
	if item.RFCState != models.RFCStateTimedOut {
		t.Errorf("got %q, want TIMED_OUT", item.RFCState)
	}

	dead := RunListItem{Task: models.Task{Status: "Failed", RetryIndex: models.MaxRetries}}
	dead.setRFCState()
	if dead.RFCState != models.RFCStateDead {
		t.Errorf("got %q, want DEAD", dead.RFCState)
	}

	running := RunListItem{Task: models.Task{Status: "Running"}}
	running.setRFCState()
	if running.RFCState != models.RFCStateRunning {
		t.Errorf("got %q, want RUNNING", running.RFCState)
	}
}

// Batch 7 (RFC-010 §22): "retrying chains" on the overview counts chains with a retry run in flight, once per chain.
func TestGetOverview_CountsChainsWithARetryInFlight(t *testing.T) {
	chainA, chainB, chainDone, chainFirst := "b7-chain-a-"+ulid.Make().String(), "b7-chain-b-"+ulid.Make().String(), "b7-chain-done-"+ulid.Make().String(), "b7-chain-first-"+ulid.Make().String()
	mk := func(chain, parent, status string) models.Task {
		return models.Task{JobId: ulid.Make().String(), ExecutionChainId: chain, ParentRunId: parent, Status: status, TaskName: "b7", TaskType: "Default"}
	}
	rows := []models.Task{
		mk(chainA, "", "Failed"), mk(chainA, "p", "Pending"), mk(chainA, "p2", "Queued"), // two retries in flight in ONE chain: counts once
		mk(chainB, "p", "Running"),
		mk(chainDone, "p", "Failed"), mk(chainDone, "", "Completed"), // retried, nothing in flight any more
		mk(chainFirst, "", "Running"), mk(chainFirst, "", "Pending"), // a FIRST run in flight is not a retry
	}
	for i := range rows {
		database.DB.Create(&rows[i])
	}
	t.Cleanup(func() {
		database.DB.Unscoped().Where("execution_chain_id IN ?", []string{chainA, chainB, chainDone, chainFirst}).Delete(&models.Task{})
	})

	count := func() int64 {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.GET("/overview", GetOverview)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/overview", nil))
		var body OverviewResponse
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.RetryingChains
	}
	with := count()

	database.DB.Unscoped().Where("execution_chain_id IN ?", []string{chainA, chainB, chainFirst}).Delete(&models.Task{})
	without := count()
	if with-without != 2 {
		t.Fatalf("two chains (A once, B once) had a retry in flight, but the count moved by %d", with-without)
	}
}

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internal/database"
)

// Not deep tests -- just proving these two brand-new endpoints (built this
// afternoon, zero coverage before this file) return 200 and a shape that
// roughly matches their response struct. Depth can come later; right now
// there is exactly zero protection against either of these breaking.

func TestGetMonitoringHealth_ReturnsOK(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/monitoring/health", nil)

	GetMonitoringHealth(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var body struct {
		SweepStatus string `json:"sweep_status"`
		Subsystems  []struct {
			Subsystem string `json:"subsystem"`
			Available bool   `json:"available"`
		} `json:"subsystems"`
		KnownGaps []string `json:"known_gaps"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response did not parse as MonitoringHealthResponse: %v, body: %s", err, w.Body.String())
	}

	if len(body.Subsystems) != 5 {
		t.Errorf("got %d subsystem rows, want 5 (Event Ingestion, Projection, Redis Queue Inspection, Alert Evaluation, PII Scanner)", len(body.Subsystems))
	}
	if len(body.KnownGaps) == 0 {
		t.Error("known_gaps is empty -- the documented gaps list should always contain at least the permanent ones (scanner drift N/A, FailedChecks count-only, post-execution scanning blocked)")
	}
}

func TestGetOverview_ReturnsOK(t *testing.T) {
	database.DB.Exec("DELETE FROM tasks")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/overview", nil)

	GetOverview(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var body struct {
		ActiveRuns int64 `json:"active_runs"`
		Live       struct {
			Watermark int64 `json:"watermark"`
		} `json:"live"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response did not parse as OverviewResponse: %v, body: %s", err, w.Body.String())
	}

	if body.ActiveRuns != 0 {
		t.Errorf("active_runs = %d, want 0 on an empty tasks table", body.ActiveRuns)
	}
}

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// RFC-010 §24: the live pipeline reports on itself through /monitoring/health.
func TestGetMonitoringHealth_IncludesLivePipeline(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/monitoring/health", nil)

	GetMonitoringHealth(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var body struct {
		LivePipeline map[string]interface{} `json:"live_pipeline"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response did not parse: %v", err)
	}
	if body.LivePipeline == nil {
		t.Fatalf("expected a live_pipeline object, got: %s", w.Body.String())
	}

	for _, key := range []string{
		"active_connections", "connections_total", "disconnections_total",
		"events_published_total", "events_delivered_total",
		"slow_clients_disconnected_total", "replays_total", "resyncs_total",
		"max_client_buffer_depth", "client_buffer_capacity",
		"last_delivery_lag_ms", "max_delivery_lag_ms",
	} {
		if _, ok := body.LivePipeline[key]; !ok {
			t.Errorf("live_pipeline is missing %q", key)
		}
	}
}

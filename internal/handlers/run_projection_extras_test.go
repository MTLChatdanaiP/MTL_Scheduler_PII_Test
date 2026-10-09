package handlers

import (
	"encoding/json"
	"testing"

	"MTL_Scheduler_PII_Test/internal/models"
)

func TestProjectionExtras_CopiesEveryNewProjectionField(t *testing.T) {
	p := models.RunProjection{
		LatestAttemptID: "A2", LatestAttemptStatus: "Failed", LatestWorkerID: "w-9", AttemptCount: 2,
		ActiveAnnotationCount: 3, OpenAlertCount: 1, Contradicted: true, ContradictionNote: "completed after Failed",
	}

	got := projectionExtras(p)

	want := RunProjectionExtras{"A2", "Failed", "w-9", 2, 3, 1, true, "completed after Failed"}
	if got != want {
		t.Fatalf("got %+v, want %+v: a field added to the projection is invisible to the dashboard until it is copied here", got, want)
	}
}

// the dashboard reads these exact keys, flat, next to the existing ones
func TestProjectionExtras_JSONKeysAreFlatWhenEmbedded(t *testing.T) {
	type item struct {
		CurrentStatus string `json:"current_status"`
		RunProjectionExtras
	}
	blob, _ := json.Marshal(item{CurrentStatus: "Failed", RunProjectionExtras: projectionExtras(models.RunProjection{Contradicted: true, OpenAlertCount: 2})})

	var m map[string]interface{}
	json.Unmarshal(blob, &m)
	for _, key := range []string{"current_status", "latest_attempt_id", "latest_attempt_status", "latest_worker_id", "attempt_count", "active_annotation_count", "open_alert_count", "contradicted", "contradiction_note"} {
		if _, ok := m[key]; !ok {
			t.Errorf("JSON key %q is missing: %s", key, blob)
		}
	}
	if m["contradicted"] != true || m["open_alert_count"] != float64(2) {
		t.Fatalf("values must survive: %s", blob)
	}
}

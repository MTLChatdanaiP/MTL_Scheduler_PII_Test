package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

// Regression test for the real bug: every process restart created a new
// Worker row with the same WorkerId, and GetWorkers returned all of them
// unreduced -- 50 rows for 3 real workers, which crashed the frontend's
// {#each workers as w (w.WorkerId)} on a duplicate key. Fixed by reducing to
// the newest row per WorkerId, same pattern GetQueues already used.
func TestGetWorkers_DeduplicatesByWorkerId_KeepsNewestOnly(t *testing.T) {
	database.DB.Exec("DELETE FROM workers")

	older := time.Now().UTC().Add(-2 * time.Hour)
	newer := time.Now().UTC().Add(-10 * time.Minute)

	// Same WorkerId, three "restarts" -- exactly what produced the real bug.
	database.DB.Create(&models.Worker{WorkerId: "Consumer-a", InstanceId: "instance-1", StartedAt: older})
	database.DB.Create(&models.Worker{WorkerId: "Consumer-a", InstanceId: "instance-2", StartedAt: older.Add(time.Hour)})
	database.DB.Create(&models.Worker{WorkerId: "Consumer-a", InstanceId: "instance-3", StartedAt: newer})

	// A genuinely different worker must still show up as its own row.
	database.DB.Create(&models.Worker{WorkerId: "Schedule_Buddy", InstanceId: "instance-4", StartedAt: newer})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/workers", nil)

	GetWorkers(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var body struct {
		Workers []struct {
			WorkerId   string `json:"WorkerId"`
			InstanceId string `json:"InstanceId"`
		} `json:"workers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response: %v, body: %s", err, w.Body.String())
	}

	if len(body.Workers) != 2 {
		t.Fatalf("got %d workers, want 2 distinct WorkerIds (Consumer-a, Schedule_Buddy) -- the dedup regressed if this is higher", len(body.Workers))
	}

	for _, worker := range body.Workers {
		if worker.WorkerId == "Consumer-a" && worker.InstanceId != "instance-3" {
			t.Errorf("Consumer-a resolved to instance %q, want the newest (instance-3)", worker.InstanceId)
		}
	}
}

package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/pii"
)

// ASSUMPTION FLAGGED same as pagination_test.go -- reuses env-var connection
// since this package's real TestMain (if one exists elsewhere in package
// handlers) wasn't visible to write this against. If handlers already has a
// TestMain, delete this one and rely on that instead -- Go allows only one
// TestMain per package.
func TestMain(m *testing.M) {
	godotenv.Load("../../.env")
	database.ConnectDatabase()

	database.DB.AutoMigrate(&models.Task{}, &models.Worker{})

	if _, err := pii.ActivatePolicy(context.Background(), "../../policies/default.json", "STARTUP", "system"); err != nil {
		panic("handlers_test: failed to activate PII policy: " + err.Error())
	}

	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func TestGetTask_RejectsNonNumericDurationFrom(t *testing.T) {
	database.DB.Exec("DELETE FROM tasks")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/runs?duration_from=notanumber", nil)

	GetTask(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a non-numeric duration_from", w.Code)
	}
}

func TestGetTask_RejectsNonNumericDurationTo(t *testing.T) {
	database.DB.Exec("DELETE FROM tasks")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/runs?duration_to=abc", nil)

	GetTask(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a non-numeric duration_to", w.Code)
	}
}

func TestGetTask_AcceptsNumericDurationFrom(t *testing.T) {
	database.DB.Exec("DELETE FROM tasks")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/runs?duration_from=30", nil)

	GetTask(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for a valid numeric duration_from, body: %s", w.Code, w.Body.String())
	}
}

func TestGetTask_AcceptsDecimalDuration(t *testing.T) {
	database.DB.Exec("DELETE FROM tasks")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/runs?duration_from=1.5", nil)

	GetTask(c)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 -- duration is parsed as a float (ParseFloat), 1.5 seconds must be valid", w.Code)
	}
}

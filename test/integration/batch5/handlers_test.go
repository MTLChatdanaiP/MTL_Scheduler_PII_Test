package batch5_test

// Batch 5 (5A) through the real Gin handlers: who toggled a schedule or re-ran a run is recorded, and the debug route exists
// only when it is switched on. The destructive ResetAllData handler is deliberately NOT called here (it would wipe the test
// database); its gating is covered by the route tests and by test/lint.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/oklog/ulid/v2"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/handlers"
	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/routes"
)

func callHandler(h gin.HandlerFunc, body string, params gin.Params, actor string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = params
	if actor != "" {
		c.Set("actor", actor)
	}
	h(c)
	return w
}

func auditRows(subjectID, action string) []models.AuditRecord {
	var rows []models.AuditRecord
	database.DB.Where("subject_id = ? AND action = ?", subjectID, action).Find(&rows)
	return rows
}

func newSchedule(t *testing.T) string {
	t.Helper()
	b5DB(t)
	id := "b5-sched-" + ulid.Make().String()
	if err := database.DB.Create(&models.ScheduleDefinition{ScheduleId: id, Enabled: true}).Error; err != nil {
		t.Fatalf("cannot create the schedule: %v", err)
	}
	t.Cleanup(func() {
		database.DB.Unscoped().Where("schedule_id = ?", id).Delete(&models.ScheduleDefinition{})
		database.DB.Unscoped().Where("subject_id = ?", id).Delete(&models.AuditRecord{})
	})
	return id
}

func TestToggleSchedule_RecordsWhoDidItInBothDirections(t *testing.T) {
	id := newSchedule(t)

	if w := callHandler(handlers.ToggleSchedule, `{"enabled":false}`, gin.Params{{Key: "schedule_id", Value: id}}, "alice"); w.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", w.Code, w.Body.String())
	}
	if rows := auditRows(id, "SCHEDULE_DISABLED"); len(rows) != 1 || rows[0].Actor != "alice" || rows[0].SubjectType != "SCHEDULE" {
		t.Fatalf("disable must be audited with the actor: %+v", rows)
	}

	if w := callHandler(handlers.ToggleSchedule, `{"enabled":true}`, gin.Params{{Key: "schedule_id", Value: id}}, "bob"); w.Code != http.StatusOK {
		t.Fatalf("enable: %d", w.Code)
	}
	if rows := auditRows(id, "SCHEDULE_ENABLED"); len(rows) != 1 || rows[0].Actor != "bob" {
		t.Fatalf("enable must be audited with the actor: %+v", rows)
	}

	var def models.ScheduleDefinition
	database.DB.Where("schedule_id = ?", id).First(&def)
	if !def.Enabled {
		t.Fatal("the toggle itself must still work")
	}
}

func TestToggleSchedule_AnUnknownScheduleIs404AndLeavesNoAuditRecord(t *testing.T) {
	b5DB(t)
	id := "b5-missing-" + ulid.Make().String()
	w := callHandler(handlers.ToggleSchedule, `{"enabled":false}`, gin.Params{{Key: "schedule_id", Value: id}}, "alice")
	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d", w.Code)
	}
	if len(auditRows(id, "SCHEDULE_DISABLED")) != 0 {
		t.Fatal("nothing changed, so nothing may be audited as changed")
	}
}

func TestRerunTaskPost_RecordsWhoDidIt(t *testing.T) {
	original := b5Create(t, "n", `{"ok":"fine"}`)
	database.DB.Model(&models.Task{}).Where("job_id = ?", original.JobId).UpdateColumn("status", "Completed") // only finished runs can be re-run

	w := callHandler(handlers.RerunTaskPost, ``, gin.Params{{Key: "job_id", Value: original.JobId}}, "carol")
	if w.Code != http.StatusCreated {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	t.Cleanup(func() {
		var rerun models.Task
		if database.DB.Where("source_run_id = ?", original.JobId).First(&rerun).Error == nil {
			database.DB.Unscoped().Where("job_id = ?", rerun.JobId).Delete(&models.Task{})
			database.DB.Unscoped().Where("job_id = ?", rerun.JobId).Delete(&models.EventEnvelope{})
		}
		database.DB.Unscoped().Where("subject_id = ?", original.JobId).Delete(&models.AuditRecord{})
	})

	if rows := auditRows(original.JobId, "RUN_RERUN"); len(rows) != 1 || rows[0].Actor != "carol" || rows[0].SubjectType != "RUN" {
		t.Fatalf("the re-run must be audited with the actor: %+v", rows)
	}
}

func routeExists(r *gin.Engine, method, path string) bool {
	for _, route := range r.Routes() {
		if route.Method == method && route.Path == path {
			return true
		}
	}
	return false
}

func TestSetupRouter_TheDebugRouteDoesNotExistByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("ENABLE_DEBUG_ENDPOINTS", "")
	if routeExists(routes.SetupRouter(context.Background()), http.MethodDelete, "/debug/reset") {
		t.Fatal("DELETE /debug/reset must not be registered unless ENABLE_DEBUG_ENDPOINTS=true")
	}
}

func TestSetupRouter_TheDebugRouteExistsOnlyWhenSwitchedOn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("ENABLE_DEBUG_ENDPOINTS", "true")
	if !routeExists(routes.SetupRouter(context.Background()), http.MethodDelete, "/debug/reset") {
		t.Fatal("with ENABLE_DEBUG_ENDPOINTS=true the route should exist")
	}
	t.Setenv("ENABLE_DEBUG_ENDPOINTS", "yes")
	if routeExists(routes.SetupRouter(context.Background()), http.MethodDelete, "/debug/reset") {
		t.Fatal("only the exact value 'true' switches it on")
	}
}

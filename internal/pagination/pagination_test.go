package pagination

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

// ============================================================================
// TestMain -- ASSUMPTION FLAGGED: this reconnects using the same env vars
// tests.yml sets (DB_HOST/PORT/USER/PASSWORD/NAME), since pagination has no
// prior test file and therefore no existing TestMain to reuse. If your
// project already has a shared test-DB-connect helper (taskservice_test.go's
// TestMain likely has one), replace this whole block with a call to that
// instead -- don't run two separate connection setups if one already exists.
// ============================================================================

func TestMain(m *testing.M) {
	godotenv.Load("../../.env")
	database.ConnectDatabase()

	database.DB.AutoMigrate(&models.Task{}, &models.Alert{})

	os.Exit(m.Run())
}

func resetTables(t *testing.T) {
	t.Helper()
	database.DB.Exec("DELETE FROM tasks")
	database.DB.Exec("DELETE FROM alerts")
}

func testGinContext(query string) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/runs?"+query, nil)
	c.Request = req
	return c
}

// ---------------------------------------------------------------------------
// ParseParams
// ---------------------------------------------------------------------------

func TestParseParams_DefaultsLimitWhenAbsent(t *testing.T) {
	p, err := ParseParams(testGinContext(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Limit != defaultLimit {
		t.Errorf("Limit = %d, want default %d", p.Limit, defaultLimit)
	}
}

func TestParseParams_RejectsBothOffsetAndCursor(t *testing.T) {
	_, err := ParseParams(testGinContext("offset=0&cursor=abc"))
	if err == nil {
		t.Fatal("expected an error when both offset and cursor are set, got nil")
	}
}

func TestParseParams_RejectsNegativeOffset(t *testing.T) {
	_, err := ParseParams(testGinContext("offset=-5"))
	if err == nil {
		t.Fatal("expected an error for a negative offset, got nil")
	}
}

func TestParseParams_RejectsZeroOrNegativeLimit(t *testing.T) {
	for _, v := range []string{"0", "-10"} {
		_, err := ParseParams(testGinContext("limit=" + v))
		if err == nil {
			t.Errorf("expected an error for limit=%s, got nil", v)
		}
	}
}

func TestParseParams_ClampsLimitAboveMax(t *testing.T) {
	p, err := ParseParams(testGinContext("limit=99999"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Limit != maxLimit {
		t.Errorf("Limit = %d, want clamped to %d", p.Limit, maxLimit)
	}
}

func TestParseParams_SetsModeCorrectly(t *testing.T) {
	p, _ := ParseParams(testGinContext("offset=0"))
	if p.Mode != PaginationOffset {
		t.Errorf("Mode = %q, want %q", p.Mode, PaginationOffset)
	}

	p, _ = ParseParams(testGinContext(""))
	if p.Mode != PaginationCursor {
		t.Errorf("Mode with no params = %q, want %q (a first cursor-mode request sends no cursor at all)", p.Mode, PaginationCursor)
	}
}

// ---------------------------------------------------------------------------
// EncodeCursor / DecodeCursor
// ---------------------------------------------------------------------------

func TestEncodeDecodeCursor_RoundTrips(t *testing.T) {
	original := time.Date(2026, 9, 22, 14, 30, 7, 881000000, time.UTC)
	var id uint = 4471

	cursor := EncodeCursor(original, id)
	gotTime, gotID, err := DecodeCursor(cursor)
	if err != nil {
		t.Fatalf("DecodeCursor failed on a cursor we just encoded: %v", err)
	}
	if !gotTime.Equal(original) {
		t.Errorf("decoded time = %v, want %v", gotTime, original)
	}
	if gotID != id {
		t.Errorf("decoded id = %d, want %d", gotID, id)
	}
}

func TestDecodeCursor_RejectsGarbageWithoutPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DecodeCursor panicked on garbage input: %v", r)
		}
	}()

	for _, bad := range []string{"", "not-base64!!!", "aGVsbG8=", "aGVsbG98d29ybGQ="} {
		_, _, err := DecodeCursor(bad)
		if err == nil {
			t.Errorf("DecodeCursor(%q) returned no error on invalid input", bad)
		}
	}
}

// ---------------------------------------------------------------------------
// ApplyPagination
// ---------------------------------------------------------------------------

// THE REGRESSION TEST. ApplyPagination used to hardcode Count against
// models.Task regardless of what was actually being paginated -- GET /alerts
// was silently reporting the TASK count as its alert total. This proves the
// fix holds: paginating Alerts must report the ALERT count.
func TestApplyPagination_CountsTheModelPassedIn_NotHardcodedTask(t *testing.T) {
	resetTables(t)

	// Seed a different number of tasks vs alerts specifically so a
	// regression (falling back to counting Task) is impossible to miss.
	for i := 0; i < 10; i++ {
		database.DB.Create(&models.Task{JobId: "task-" + string(rune('a'+i)), TaskType: "TEST"})
	}
	for i := 0; i < 3; i++ {
		database.DB.Create(&models.Alert{AlertID: "alert-" + string(rune('a'+i)), AlertType: "TEST", Status: "OPEN", OpenedAt: time.Now().UTC()})
	}

	p, _ := ParseParams(testGinContext("offset=0&limit=50"))
	query := database.DB.Model(&models.Alert{})

	query, total, err := ApplyPagination(query, p, "created_at", &models.Alert{})
	if err != nil {
		t.Fatalf("ApplyPagination failed: %v", err)
	}

	var results []models.Alert
	if err := query.Find(&results).Error; err != nil {
		t.Fatalf("Find failed: %v", err)
	}

	if total == nil {
		t.Fatal("total is nil in offset mode")
	}
	if *total != 3 {
		t.Errorf("total = %d, want 3 (the alert count) -- got the task count instead if this reads 10", *total)
	}
}

// Proves the actual point of cursor pagination: an insert that happens
// BETWEEN two pages must not produce a duplicate row in cursor mode, the way
// it would in offset mode.
func TestApplyPagination_CursorMode_NoDuplicateOnInsertBetweenPages(t *testing.T) {
	resetTables(t)

	for i := 0; i < 5; i++ {
		task := models.Task{
			JobId:    "job-" + string(rune('a'+i)),
			TaskType: "TEST",
		}
		database.DB.Create(&task)
		time.Sleep(10 * time.Millisecond) // ensures CreatedAt strictly increases between rows
	}

	// page 1
	p1, _ := ParseParams(testGinContext("limit=2"))
	q1, _, err := ApplyPagination(database.DB.Model(&models.Task{}), p1, "created_at", &models.Task{})
	if err != nil {
		t.Fatalf("page 1 ApplyPagination failed: %v", err)
	}
	var page1 []models.Task
	q1.Find(&page1)
	if len(page1) != 2 {
		t.Fatalf("page 1 returned %d rows, want 2", len(page1))
	}
	cursor := EncodeCursor(page1[len(page1)-1].CreatedAt, page1[len(page1)-1].ID)

	// simulate a new row arriving between page loads -- newer than
	// everything seeded so far
	newTask := models.Task{JobId: "job-new", TaskType: "TEST"}
	database.DB.Create(&newTask)

	// page 2, using the cursor captured before the insert above
	p2, _ := ParseParams(testGinContext("limit=2&cursor=" + cursor))
	q2, _, err := ApplyPagination(database.DB.Model(&models.Task{}), p2, "created_at", &models.Task{})
	if err != nil {
		t.Fatalf("page 2 ApplyPagination failed: %v", err)
	}
	var page2 []models.Task
	q2.Find(&page2)

	seenOnPage1 := make(map[string]bool)
	for _, task := range page1 {
		seenOnPage1[task.JobId] = true
	}
	for _, task := range page2 {
		if seenOnPage1[task.JobId] {
			t.Errorf("cursor pagination returned %q again on page 2 -- it was already on page 1", task.JobId)
		}
	}
	if len(page2) == 0 {
		t.Error("page 2 returned no rows -- expected the remaining seeded tasks")
	}
}

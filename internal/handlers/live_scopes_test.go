package handlers

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"MTL_Scheduler_PII_Test/internal/auth"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/live"
	"MTL_Scheduler_PII_Test/internal/models"
)

// ---------------------------------------------------------------------------
// Pure unit tests (no database work)
// ---------------------------------------------------------------------------

func TestRequiredScope(t *testing.T) {
	cases := map[string]string{
		"alert.opened":              "alerts.read",
		"alert.notification_failed": "alerts.read",
		"pii.detected":              "pii.findings.read",
		"pii.encryption.failed":     "pii.findings.read",
		"pii.policy_activated":      "pii.policy.read",
		"pii.policy_reload_failed":  "pii.policy.read",
		"task.created":              "",
		"task.completed":            "",
		"worker.online":             "",
		// lookalikes that must NOT be caught by the prefixes
		"alerts.x": "",
		"piix.y":   "",
		"":         "",
	}

	for eventType, want := range cases {
		if got := live.RequiredScope(eventType); got != want {
			t.Errorf("requiredScope(%q) = %q, want %q", eventType, got, want)
		}
	}
}

func TestHiddenEventSQL(t *testing.T) {
	all := func(string) bool { return true }
	if got := live.HiddenEventSQL(all); got != "" {
		t.Errorf("a caller with every scope must have nothing hidden, got %q", got)
	}

	none := func(string) bool { return false }
	got := live.HiddenEventSQL(none)
	for _, prefix := range []string{"alert.", "pii.policy", "pii."} {
		if !strings.Contains(got, "LIKE '"+prefix+"%'") {
			t.Errorf("caller with no extra scopes: expected %q to be hidden, got %q", prefix, got)
		}
	}

	// pii.policy.read alone must NOT hide policy events, only findings + alerts.
	onlyPolicy := func(s string) bool { return s == "pii.policy.read" }
	got = live.HiddenEventSQL(onlyPolicy)
	if strings.Contains(got, "(event_type LIKE 'pii.policy%'") {
		t.Errorf("policy events must stay visible to pii.policy.read, got %q", got)
	}
	if !strings.Contains(got, "NOT LIKE 'pii.policy%'") {
		t.Errorf("hiding pii.* must exclude pii.policy* so policy events are not hidden by the parent rule, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Replay honours scopes end to end (uses the real database and real handler)
// ---------------------------------------------------------------------------

func writeTestPrincipals(t *testing.T) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "principals.json")
	body := `[
	  {"name":"limited","key":"k-limited","scopes":["job.read"]},
	  {"name":"policy","key":"k-policy","scopes":["job.read","pii.policy.read"]},
	  {"name":"full","key":"k-full","scopes":["job.read","alerts.read","pii.findings.read","pii.policy.read"]}
	]`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing principals: %v", err)
	}
	if err := auth.LoadPrincipals(path); err != nil {
		t.Fatalf("loading principals: %v", err)
	}
}

// Seeds one row per event type plus a final sentinel and returns the cursor to
// connect from. It never deletes anything, so it is safe on a shared dev DB:
// the cursor is the current max id, so only these new rows are replayed.
func seedScopeEvents(t *testing.T) uint {
	t.Helper()

	if err := database.DB.AutoMigrate(&models.EventEnvelope{}); err != nil {
		t.Fatalf("migrating event_envelopes: %v", err)
	}

	var maxID uint
	database.DB.Model(&models.EventEnvelope{}).Select("COALESCE(MAX(id), 0)").Scan(&maxID)

	types := []string{
		"task.created", "alert.opened", "pii.detected", "pii.policy_activated",
		"task.completed", "task.scope_sentinel",
	}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	for i, typ := range types {
		e := models.EventEnvelope{
			JobId:      "job-scope-test",
			EventID:    "evt-scope-" + stamp + "-" + strconv.Itoa(i),
			EventType:  typ,
			OccurredAt: time.Now().UTC(),
			IngestedAt: time.Now().UTC(),
			Producer:   "test",
		}
		if err := database.DB.Create(&e).Error; err != nil {
			t.Fatalf("seeding %s: %v", typ, err)
		}
	}

	return maxID
}

// Reads the stream until the sentinel event arrives. Replay is in id order and
// the sentinel is the last row, so once it is seen every earlier visible row
// has already been delivered.
func typesUntilSentinel(t *testing.T, url, key string) []string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-Key", key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var seen []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "event: ") {
			continue
		}
		typ := strings.TrimPrefix(line, "event: ")
		seen = append(seen, typ)
		if typ == "task.scope_sentinel" {
			return seen
		}
	}

	t.Fatalf("stream ended before the sentinel arrived; saw %v", seen)
	return nil
}

func hasType(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestLiveReplay_HidesEventsCallerCannotRead(t *testing.T) {
	writeTestPrincipals(t)
	cursor := seedScopeEvents(t)

	serverCtx, cancel := context.WithCancel(context.Background())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/live/activity", auth.RequireScope("job.read"), GetLive(serverCtx))

	srv := httptest.NewServer(r)
	defer srv.Close() // runs second
	defer cancel()    // runs first, so open streams end and Close() does not block

	url := srv.URL + "/live/activity?after=" + strconv.FormatUint(uint64(cursor), 10)

	t.Run("job.read only sees task events, not alert or pii", func(t *testing.T) {
		seen := typesUntilSentinel(t, url, "k-limited")
		for _, want := range []string{"task.created", "task.completed"} {
			if !hasType(seen, want) {
				t.Errorf("expected %q, saw %v", want, seen)
			}
		}
		for _, hidden := range []string{"alert.opened", "pii.detected", "pii.policy_activated"} {
			if hasType(seen, hidden) {
				t.Errorf("%q leaked to a caller without its scope; saw %v", hidden, seen)
			}
		}
	})

	t.Run("pii.policy.read sees policy events but not findings or alerts", func(t *testing.T) {
		seen := typesUntilSentinel(t, url, "k-policy")
		if !hasType(seen, "pii.policy_activated") {
			t.Errorf("policy event should be visible, saw %v", seen)
		}
		for _, hidden := range []string{"alert.opened", "pii.detected"} {
			if hasType(seen, hidden) {
				t.Errorf("%q leaked to a caller without its scope; saw %v", hidden, seen)
			}
		}
	})

	t.Run("caller with every scope sees everything", func(t *testing.T) {
		seen := typesUntilSentinel(t, url, "k-full")
		for _, want := range []string{"task.created", "alert.opened", "pii.detected", "pii.policy_activated", "task.completed"} {
			if !hasType(seen, want) {
				t.Errorf("expected %q, saw %v", want, seen)
			}
		}
	})
}

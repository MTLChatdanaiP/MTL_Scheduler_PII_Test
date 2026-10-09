package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// Batch 6 (RFC-010 round two) end to end: real database, real handler, real SSE over HTTP.

type sseFrame struct {
	Event string
	ID    string
	Data  map[string]interface{}
	Raw   string
}

func newLiveServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	writeTestPrincipals(t)
	serverCtx, cancel := context.WithCancel(context.Background())
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/live/activity", auth.RequireScope("job.read"), GetLive(serverCtx))
	srv := httptest.NewServer(r)
	return srv, func() { cancel(); srv.Close() }
}

func maxEventID(t *testing.T) uint {
	t.Helper()
	if err := database.DB.AutoMigrate(&models.EventEnvelope{}); err != nil {
		t.Fatal(err)
	}
	var maxID uint
	database.DB.Model(&models.EventEnvelope{}).Select("COALESCE(MAX(id), 0)").Scan(&maxID)
	return maxID
}

var b6seq int

func seed(t *testing.T, eventType, subject, chain string) {
	t.Helper()
	b6seq++
	e := models.EventEnvelope{
		JobId: subject, EventID: "evt-b6-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + strconv.Itoa(b6seq),
		EventType: eventType, OccurredAt: time.Now().UTC(), IngestedAt: time.Now().UTC(), Producer: "test", ExecutionChainID: chain,
	}
	if err := database.DB.Create(&e).Error; err != nil {
		t.Fatalf("seeding %s: %v", eventType, err)
	}
}

// readFrames reads the stream until stop returns true (or the timeout passes) and returns every frame seen.
func readFrames(t *testing.T, url, key string, timeout time.Duration, stop func([]sseFrame) bool) []sseFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("X-API-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var frames []sseFrame
	cur := sseFrame{}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "id: "):
			cur.ID = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			cur.Raw = strings.TrimPrefix(line, "data: ")
			_ = json.Unmarshal([]byte(cur.Raw), &cur.Data)
		case line == "" && cur.Event != "":
			frames = append(frames, cur)
			cur = sseFrame{}
			if stop(frames) {
				return frames
			}
		}
	}
	return frames // timeout or end of stream
}

func statusOf(t *testing.T, url, key string) (int, http.Header, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("X-API-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	return resp.StatusCode, resp.Header, string(buf[:n])
}

func subjectsOf(frames []sseFrame, eventType string) []string {
	var out []string
	for _, f := range frames {
		if f.Event == eventType {
			out = append(out, f.Data["subject"].(string))
		}
	}
	return out
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestLiveB6_ScopedReplayReturnsOnlyTheSubscribedScope(t *testing.T) {
	srv, stop := newLiveServer(t)
	defer stop()
	cursor := maxEventID(t)
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	qa, qb, w := "q-a-"+stamp, "q-b-"+stamp, "w-"+stamp

	seed(t, "queue.degraded", qa, "")
	seed(t, "queue.degraded", qb, "")
	seed(t, "worker.offline", w, "")
	seed(t, "queue.healthy", qa, "") // the last in-scope row: seeing it means the replay is complete

	url := srv.URL + "/live/activity?scope=queue:" + qa + "&after=" + strconv.FormatUint(uint64(cursor), 10)
	frames := readFrames(t, url, "k-limited", 5*time.Second, func(f []sseFrame) bool { return f[len(f)-1].Event == "queue.healthy" })

	got := subjectsOf(frames, "queue.degraded")
	if len(got) != 1 || got[0] != qa {
		t.Fatalf("queue.degraded subjects = %v, want only %s", got, qa)
	}
	if len(subjectsOf(frames, "worker.offline")) != 0 {
		t.Fatal("a worker event leaked into a queue:<name> subscription")
	}
}

func TestLiveB6_ScopeSetDeliversEveryScopeInTheSet(t *testing.T) {
	srv, stop := newLiveServer(t)
	defer stop()
	cursor := maxEventID(t)
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	w1, w2, other := "w1-"+stamp, "w2-"+stamp, "w3-"+stamp

	seed(t, "worker.offline", w1, "")
	seed(t, "worker.offline", other, "")
	seed(t, "worker.online", w2, "")

	url := srv.URL + "/live/activity?scope=worker:" + w1 + "&scope=worker:" + w2 + "&after=" + strconv.FormatUint(uint64(cursor), 10)
	frames := readFrames(t, url, "k-limited", 5*time.Second, func(f []sseFrame) bool { return containsStr(subjectsOf(f, "worker.online"), w2) })

	all := append(subjectsOf(frames, "worker.offline"), subjectsOf(frames, "worker.online")...)
	if !containsStr(all, w1) || !containsStr(all, w2) || containsStr(all, other) {
		t.Fatalf("a set of worker scopes should deliver w1 and w2 but not %s; got %v", other, all)
	}
}

func TestLiveB6_ExecutionChainScopeMatchesByChainID(t *testing.T) {
	srv, stop := newLiveServer(t)
	defer stop()
	cursor := maxEventID(t)
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	chainX, chainY := "chain-x-"+stamp, "chain-y-"+stamp

	seed(t, "task.created", "run-x1-"+stamp, chainX)
	seed(t, "task.created", "run-y1-"+stamp, chainY)
	seed(t, "task.completed", "run-x1-"+stamp, chainX)

	url := srv.URL + "/live/activity?scope=execution-chain:" + chainX + "&after=" + strconv.FormatUint(uint64(cursor), 10)
	frames := readFrames(t, url, "k-limited", 5*time.Second, func(f []sseFrame) bool { return f[len(f)-1].Event == "task.completed" })

	if len(frames) != 2 {
		t.Fatalf("chain scope should deliver exactly the two events of chain X, got %d: %+v", len(frames), frames)
	}
	for _, f := range frames {
		if f.Data["execution_chain_id"] != chainX || f.Data["resource_type"] != "RUN" {
			t.Errorf("unexpected frame %+v", f.Data)
		}
	}
}

func TestLiveB6_ReplayedFramesCarryTheEnvelope(t *testing.T) {
	srv, stop := newLiveServer(t)
	defer stop()
	cursor := maxEventID(t)
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	q := "q-env-" + stamp
	seed(t, "queue.no_consumer", q, "")

	url := srv.URL + "/live/activity?scope=queue:" + q + "&after=" + strconv.FormatUint(uint64(cursor), 10)
	frames := readFrames(t, url, "k-limited", 5*time.Second, func(f []sseFrame) bool { return true })
	if len(frames) != 1 {
		t.Fatalf("frames = %d", len(frames))
	}
	d := frames[0].Data
	if d["resource_type"] != "QUEUE" || d["change_type"] != "HEALTH_CHANGED" || d["payload_mode"] != "NONE" || d["schema_version"] != float64(1) {
		t.Errorf("envelope = %v", d)
	}
	if d["change_seq"] != d["id"] || frames[0].ID != strconv.FormatInt(int64(d["id"].(float64)), 10) {
		t.Errorf("change_seq must equal the event id and the SSE id line: %v / id line %q", d, frames[0].ID)
	}
	if d["freshness"].(map[string]interface{})["status"] != "REPLAYED" {
		t.Errorf("a replayed event is REPLAYED, got %v", d["freshness"])
	}
	for _, legacy := range []string{"id", "type", "subject", "at"} {
		if _, ok := d[legacy]; !ok {
			t.Errorf("legacy key %q must still be present", legacy)
		}
	}
}

func TestLiveB6_BadScopeIs400AndUnauthorizedScopeIs403(t *testing.T) {
	srv, stop := newLiveServer(t)
	defer stop()

	if code, _, _ := statusOf(t, srv.URL+"/live/activity?scope=bogus", "k-full"); code != http.StatusBadRequest {
		t.Errorf("unknown scope: status %d, want 400", code)
	}
	if code, _, _ := statusOf(t, srv.URL+"/live/activity?scope=worker:", "k-full"); code != http.StatusBadRequest {
		t.Errorf("scope with an empty id: status %d, want 400", code)
	}

	// authorization applies to the EFFECTIVE subscription: a key without alerts.read cannot subscribe to alerts
	code, _, body := statusOf(t, srv.URL+"/live/activity?scope=alerts", "k-limited")
	if code != http.StatusForbidden || !strings.Contains(body, "alerts.read") {
		t.Errorf("alerts scope without alerts.read: status %d body %q, want 403 naming the scope", code, body)
	}
	if code, _, _ := statusOf(t, srv.URL+"/live/activity?scope=pii.summary", "k-limited"); code != http.StatusForbidden {
		t.Errorf("pii.summary without pii.findings.read: status %d, want 403", code)
	}

	// and a key that has the scope is let in
	frames := readFrames(t, srv.URL+"/live/activity?scope=alerts&scope=pii.summary", "k-full", 400*time.Millisecond, func([]sseFrame) bool { return false })
	_ = frames // the stream opened (readFrames fails the test on a non-200)
}

func TestLiveB6_ReplayDoesNotStreamNoiseTypes(t *testing.T) {
	srv, stop := newLiveServer(t)
	defer stop()
	cursor := maxEventID(t)
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)

	for i := 0; i < 5; i++ {
		seed(t, "attempt.heartbeat", "run-hb-"+stamp, "")
	}
	seed(t, "task.progress", "run-hb-"+stamp, "")
	seed(t, "task.scope_sentinel", "run-hb-"+stamp, "")

	url := srv.URL + "/live/activity?after=" + strconv.FormatUint(uint64(cursor), 10)
	frames := readFrames(t, url, "k-limited", 5*time.Second, func(f []sseFrame) bool { return f[len(f)-1].Event == "task.scope_sentinel" })
	for _, f := range frames {
		if f.Event == "attempt.heartbeat" || f.Event == "task.progress" {
			t.Fatalf("noise type %s was replayed; it is never streamed live and must not eat the 500-row cap", f.Event)
		}
	}
}

func TestLiveB6_ResyncFrameHasATypedBody(t *testing.T) {
	srv, stop := newLiveServer(t)
	defer stop()
	cursor := maxEventID(t)
	subject := "q-resync-" + strconv.FormatInt(time.Now().UnixNano(), 10)

	rows := make([]models.EventEnvelope, 0, replayCap+1)
	for i := 0; i <= replayCap; i++ {
		rows = append(rows, models.EventEnvelope{
			JobId: subject, EventID: subject + "-" + strconv.Itoa(i), EventType: "queue.degraded",
			OccurredAt: time.Now().UTC(), IngestedAt: time.Now().UTC(), Producer: "test",
		})
	}
	if err := database.DB.CreateInBatches(&rows, 200).Error; err != nil {
		t.Fatal(err)
	}
	defer database.DB.Unscoped().Where("job_id = ?", subject).Delete(&models.EventEnvelope{})

	url := srv.URL + "/live/activity?scope=queue:" + subject + "&after=" + strconv.FormatUint(uint64(cursor), 10)
	frames := readFrames(t, url, "k-limited", 5*time.Second, func(f []sseFrame) bool { return f[len(f)-1].Event == "resync" })
	last := frames[len(frames)-1]
	if last.Event != "resync" || last.Data["change_type"] != "RESYNC_REQUIRED" || last.Data["reason"] != "REPLAY_GAP" {
		t.Fatalf("resync frame = %+v", last)
	}
}

func TestLiveB6_ScopedReplayDoesNotLetOtherScopesEatTheCap(t *testing.T) {
	srv, stop := newLiveServer(t)
	defer stop()
	cursor := maxEventID(t)
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	noisy, wanted := "q-noisy-"+stamp, "q-wanted-"+stamp

	rows := make([]models.EventEnvelope, 0, replayCap+10)
	for i := 0; i < replayCap+10; i++ {
		rows = append(rows, models.EventEnvelope{
			JobId: noisy, EventID: noisy + "-" + strconv.Itoa(i), EventType: "queue.degraded",
			OccurredAt: time.Now().UTC(), IngestedAt: time.Now().UTC(), Producer: "test",
		})
	}
	if err := database.DB.CreateInBatches(&rows, 200).Error; err != nil {
		t.Fatal(err)
	}
	defer database.DB.Unscoped().Where("job_id = ?", noisy).Delete(&models.EventEnvelope{})
	seed(t, "queue.degraded", wanted, "")

	url := srv.URL + "/live/activity?scope=queue:" + wanted + "&after=" + strconv.FormatUint(uint64(cursor), 10)
	frames := readFrames(t, url, "k-limited", 5*time.Second, func(f []sseFrame) bool { return true })
	if len(frames) != 1 || frames[0].Event != "queue.degraded" {
		t.Fatalf("510 rows of another queue must not force a resync for a one-queue subscription, got %+v", frames)
	}
}

func TestLiveB6_ConnectionCapIs429ThenFreesUp(t *testing.T) {
	srv, stop := newLiveServer(t)
	defer stop()
	t.Setenv("LIVE_MAX_CONNECTIONS_PER_KEY", "2")

	open := func() context.CancelFunc {
		ctx, cancel := context.WithCancel(context.Background())
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/live/activity", nil)
		req.Header.Set("X-API-Key", "k-policy")
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			cancel()
			t.Fatalf("expected an open stream, got err=%v", err)
		}
		return func() { cancel(); resp.Body.Close() }
	}

	rejectedBefore := live.Snapshot().RejectedSubscriptionsTotal
	c1 := open()
	c2 := open()
	defer c2()

	code, hdr, _ := statusOf(t, srv.URL+"/live/activity", "k-policy")
	if code != http.StatusTooManyRequests || hdr.Get("Retry-After") == "" {
		t.Fatalf("third connection: status %d Retry-After %q, want 429 with a Retry-After", code, hdr.Get("Retry-After"))
	}
	if live.Snapshot().RejectedSubscriptionsTotal != rejectedBefore+1 {
		t.Error("the refusal must be counted in live_pipeline")
	}
	// a different key is unaffected
	if code, _, _ := statusOf(t, srv.URL+"/live/activity?scope=nothing-valid", "k-full"); code == http.StatusTooManyRequests {
		t.Error("another API key must not be limited by k-policy's connections")
	}

	c1() // close one
	deadline := time.Now().Add(3 * time.Second)
	for live.Limiter.Open("policy") > 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	c3 := open() // slot freed
	c3()
}

func TestLiveB6_PlatformSummaryReachesOnlyClientsThatAskedForIt(t *testing.T) {
	srv, stop := newLiveServer(t)
	defer stop()

	type result struct{ frames []sseFrame }
	summaryCh := make(chan result, 1)
	legacyCh := make(chan result, 1)

	go func() {
		f := readFrames(t, srv.URL+"/live/activity?scope=platform.summary", "k-limited", 4*time.Second,
			func(f []sseFrame) bool { return f[len(f)-1].Event == live.TypePlatformSummary })
		summaryCh <- result{f}
	}()
	go func() {
		f := readFrames(t, srv.URL+"/live/activity", "k-full", 2500*time.Millisecond, func(f []sseFrame) bool { return false })
		legacyCh <- result{f}
	}()

	time.Sleep(300 * time.Millisecond) // both connected
	live.NotifySummaryChanged()

	sum := (<-summaryCh).frames
	if len(sum) == 0 || sum[len(sum)-1].Event != live.TypePlatformSummary {
		t.Fatalf("a platform.summary subscriber must receive the summary, got %+v", sum)
	}
	last := sum[len(sum)-1]
	if last.ID != "" || last.Data["resource_type"] != "PLATFORM_SUMMARY" || last.Data["change_type"] != "INVALIDATE" {
		t.Errorf("summary frame = %+v (no id line: it is not a recorded fact)", last)
	}

	for _, f := range (<-legacyCh).frames {
		if f.Event == live.TypePlatformSummary {
			t.Fatal("a client that did not ask for platform.summary must never receive it")
		}
	}
}

func TestLiveB6_ReconnectWithMissedRowsGetsASummaryFrame(t *testing.T) {
	srv, stop := newLiveServer(t)
	defer stop()
	cursor := maxEventID(t)
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	q := "q-sum-" + stamp
	seed(t, "queue.degraded", q, "")

	url := srv.URL + "/live/activity?scope=platform.summary&scope=queue:" + q + "&after=" + strconv.FormatUint(uint64(cursor), 10)
	frames := readFrames(t, url, "k-limited", 5*time.Second, func(f []sseFrame) bool { return f[len(f)-1].Event == live.TypePlatformSummary })

	if len(frames) != 2 || frames[0].Event != "queue.degraded" || frames[1].Event != live.TypePlatformSummary {
		t.Fatalf("want the replayed row then one summary frame, got %+v", frames)
	}
}

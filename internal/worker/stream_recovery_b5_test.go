package worker

// Batch 5 (5B): a worker survives Redis being down at start-up or being wiped, the reclaimer reports a heartbeat, and the loops
// stop on shutdown. Real Redis and database; every helper is prefixed b5.

import (
	"context"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/redis/go-redis/v9"

	"MTL_Scheduler_PII_Test/internal/cache"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

// ---------------------------------------------------------------------------------------------------------- pure

func TestSleepCtx_WaitsTheFullTimeOrStopsAtOnce(t *testing.T) {
	start := time.Now()
	if !sleepCtx(context.Background(), 60*time.Millisecond) || time.Since(start) < 55*time.Millisecond {
		t.Fatal("an uncancelled wait must last the whole time and report true")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	start = time.Now()
	if sleepCtx(ctx, 10*time.Second) || time.Since(start) > time.Second {
		t.Fatal("a cancelled wait must return false almost at once, not after 10 s")
	}
}

func b5Recorder(cancelAfter int, cancel context.CancelFunc) (backoffConfig, *[]time.Duration) {
	var waits []time.Duration
	cfg := backoffConfig{initial: 500 * time.Millisecond, max: 30 * time.Second, jitter: 0, sleep: func(ctx context.Context, d time.Duration) bool {
		waits = append(waits, d)
		if cancelAfter > 0 && len(waits) >= cancelAfter {
			cancel()
			return false
		}
		return true
	}}
	return cfg, &waits
}

func TestRetryWithBackoff_SucceedsAfterFailures(t *testing.T) {
	cfg, waits := b5Recorder(0, nil)
	calls := 0
	err := retryWithBackoff(context.Background(), func() error {
		calls++
		if calls < 4 {
			return io.ErrUnexpectedEOF
		}
		return nil
	}, cfg)
	if err != nil || calls != 4 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}
	if len(*waits) != 3 || (*waits)[0] != want[0] || (*waits)[1] != want[1] || (*waits)[2] != want[2] {
		t.Fatalf("the wait must double after every failure: %v", *waits)
	}
}

func TestRetryWithBackoff_TheWaitIsCapped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cfg, waits := b5Recorder(7, cancel)
	cfg.initial, cfg.max = time.Second, 3*time.Second

	retryWithBackoff(ctx, func() error { return io.EOF }, cfg)
	want := []time.Duration{1, 2, 3, 3, 3, 3, 3}
	for i, w := range want {
		if (*waits)[i] != w*time.Second {
			t.Fatalf("waits = %v, want 1s 2s 3s then 3s for ever", *waits)
		}
	}
}

func TestRetryWithBackoff_StopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cfg, _ := b5Recorder(2, cancel)
	calls := 0
	err := retryWithBackoff(ctx, func() error { calls++; return io.EOF }, cfg)
	if err == nil || calls > 3 {
		t.Fatalf("a cancelled retry must stop and report why: err=%v calls=%d", err, calls)
	}
}

func TestRetryWithBackoff_AnImmediateSuccessNeverWaits(t *testing.T) {
	cfg, waits := b5Recorder(0, nil)
	if err := retryWithBackoff(context.Background(), func() error { return nil }, cfg); err != nil || len(*waits) != 0 {
		t.Fatalf("err=%v waits=%v", err, *waits)
	}
}

func TestRetryWithBackoff_JitterOnlyAddsAndIsBounded(t *testing.T) {
	var seen []time.Duration
	cfg := backoffConfig{initial: time.Second, max: time.Second, jitter: 0.2, sleep: func(ctx context.Context, d time.Duration) bool { seen = append(seen, d); return len(seen) < 50 }}
	retryWithBackoff(context.Background(), func() error { return io.EOF }, cfg)
	for _, d := range seen {
		if d < time.Second || d > 1200*time.Millisecond {
			t.Fatalf("a wait of %v is outside [1s, 1.2s]", d)
		}
	}
}

// ---------------------------------------------------------------------------------------------------------- real Redis

func b5Stream() (string, string) { return "b5:stream:" + ulid.Make().String(), "b5-group" }

func TestEnsureGroup_CreatesItAndABusyGroupIsSuccess(t *testing.T) {
	stream, group := b5Stream()
	ctx := context.Background()
	t.Cleanup(func() { cache.Client.Del(ctx, stream) })

	if err := ensureGroup(ctx, cache.Client, stream, group); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := ensureGroup(ctx, cache.Client, stream, group); err != nil {
		t.Fatalf("a group that already exists is the normal case for every later worker: %v", err)
	}
	groups, _ := cache.Client.XInfoGroups(ctx, stream).Result()
	if len(groups) != 1 || groups[0].Name != group {
		t.Fatalf("groups = %+v", groups)
	}
}

func TestReadStream_RecreatesAGroupThatWasWiped(t *testing.T) {
	stream, group := b5Stream()
	ctx := context.Background()
	rdb := cache.Client
	t.Cleanup(func() { rdb.Del(ctx, stream) })

	ensureGroup(ctx, rdb, stream, group)
	rdb.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]interface{}{"job_id": "first"}})
	if got := ReadStream(ctx, "b5-consumer", stream, group); len(got) == 0 {
		t.Fatal("setup: the first read must return the message")
	}

	// Redis restarted without persistence (or the key was deleted): the stream and its groups are gone
	rdb.Del(ctx, stream)
	if got := ReadStream(ctx, "b5-consumer", stream, group); got != nil {
		t.Fatalf("nothing can be read from a wiped stream: %v", got)
	}

	// the failed read healed the group, so the next read works
	rdb.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]interface{}{"job_id": "second"}})
	got := ReadStream(ctx, "b5-consumer", stream, group)
	if len(got) == 0 || len(got[0].Messages) == 0 || got[0].Messages[0].Values["job_id"] != "second" {
		t.Fatalf("after a wipe the worker must recover on its own, got %+v", got)
	}
}

func TestStartReclaimer_ReportsAHeartbeatSoItIsNotUnknownForEver(t *testing.T) {
	id := "b5-reclaimer-" + ulid.Make().String()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { StartReclaimer(ctx, id); close(done) }()
	t.Cleanup(func() {
		cancel()
		database.DB.Unscoped().Where("worker_id = ?", id).Delete(&models.Worker{})
		database.DB.Unscoped().Where("worker_id = ?", id).Delete(&models.WorkerHeartbeat{})
	})

	deadline := time.Now().Add(3 * time.Second)
	var n int64
	for time.Now().Before(deadline) {
		database.DB.Model(&models.WorkerHeartbeat{}).Where("worker_id = ?", id).Count(&n)
		if n > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n == 0 {
		t.Fatal("the reclaimer wrote no heartbeat: it would show as UNKNOWN for ever")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the reclaimer must stop promptly on shutdown (it used to finish a time.Sleep first)")
	}
}

func TestStartReclaimer_RecreatesAGroupThatWasWiped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rdb := cache.Client
	rdb.XGroupDestroy(ctx, cache.TaskStream, WorkerGroupA) // the group the reclaimer reads from is gone
	id := "b5-reclaimer-" + ulid.Make().String()
	go StartReclaimer(ctx, id)
	t.Cleanup(func() {
		cancel()
		ensureGroup(context.Background(), rdb, cache.TaskStream, WorkerGroupA) // leave the shared group as other tests expect it
		database.DB.Unscoped().Where("worker_id = ?", id).Delete(&models.Worker{})
		database.DB.Unscoped().Where("worker_id = ?", id).Delete(&models.WorkerHeartbeat{})
	})

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if groups, err := rdb.XInfoGroups(ctx, cache.TaskStream).Result(); err == nil {
			for _, g := range groups {
				if g.Name == WorkerGroupA {
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the reclaimer must recreate the missing group instead of failing with NOGROUP for ever")
}

func TestStartHeartbeat_WithNoCounterWaitsInsteadOfSpinning(t *testing.T) {
	var calls int
	original := heartbeatWait
	heartbeatWait = func(ctx context.Context, d time.Duration) bool { calls++; return false } // "stop" after the first wait
	t.Cleanup(func() { heartbeatWait = original })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { StartHeartbeat(ctx, "b5-no-counter-"+ulid.Make().String(), "inst"); close(done) }()

	select {
	case <-done:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("with no counter registered the loop must WAIT (and then stop here), not spin at full speed until the context ends")
	}
	if calls != 1 {
		t.Fatalf("expected exactly one wait, got %d", calls)
	}
}

// ---------------------------------------------------------------------------------------------------------- Redis down at start

// b5Proxy is a TCP forwarder to the real Redis that does NOT listen until start() is called: until then the address refuses
// connections, exactly like a Redis that is down. The same client object then has to reconnect, as it would in production.
type b5Proxy struct {
	addr     string
	listener net.Listener
	wg       sync.WaitGroup
}

func newB5Proxy(t *testing.T) *b5Proxy {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot reserve a local port: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	p := &b5Proxy{addr: addr}
	t.Cleanup(p.stop)
	return p
}

func (p *b5Proxy) start(t *testing.T, target string) {
	t.Helper()
	l, err := net.Listen("tcp", p.addr)
	if err != nil {
		t.Fatalf("cannot start the proxy: %v", err)
	}
	p.listener = l
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			client, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				upstream, err := net.Dial("tcp", target)
				if err != nil {
					client.Close()
					return
				}
				go func() { io.Copy(upstream, client); upstream.Close() }()
				io.Copy(client, upstream)
				client.Close()
			}()
		}
	}()
}

func (p *b5Proxy) stop() {
	if p.listener != nil {
		p.listener.Close()
	}
}

func b5Count(eventType, producer string) int64 {
	var n int64
	database.DB.Model(&models.EventEnvelope{}).Where("event_type = ? AND producer = ?", eventType, producer).Count(&n)
	return n
}

func TestSetupWorker_StartsWhileRedisIsDownAndRecoversWhenItReturns(t *testing.T) {
	real := cache.Client
	target := os.Getenv("REDIS_ADDR")
	if target == "" {
		target = "localhost:6379"
	}
	proxy := newB5Proxy(t)
	cache.Client = redis.NewClient(&redis.Options{Addr: proxy.addr, Username: os.Getenv("REDIS_USERNAME"), Password: os.Getenv("REDIS_PASSWORD"), MaxRetries: -1, DialTimeout: 300 * time.Millisecond})
	downClient := cache.Client
	t.Cleanup(func() { cache.Client = real; downClient.Close() })

	id := "b5-setup-" + ulid.Make().String()
	ctx, cancel := context.WithCancel(context.Background())
	// start from "the group does not exist", so a group that appears can only have been created by this worker
	real.XGroupDestroy(context.Background(), cache.TaskStream, WorkerGroupA)
	unavailableBefore := b5Count("redis.unavailable", "worker")
	done := make(chan struct{})
	go func() { SetupWorker(ctx, id); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
		ensureGroup(context.Background(), real, cache.TaskStream, WorkerGroupA) // leave the shared group as other tests expect it
		database.DB.Unscoped().Where("worker_id = ?", id).Delete(&models.Worker{})
		database.DB.Unscoped().Where("worker_id = ?", id).Delete(&models.WorkerHeartbeat{})
	})

	// Redis is DOWN: the worker must keep trying, not crash, not give up, and not flood the event log
	time.Sleep(1800 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("SetupWorker returned while Redis was down: it must keep retrying")
	default:
	}
	if added := b5Count("redis.unavailable", "worker") - unavailableBefore; added > 1 {
		t.Fatalf("a Redis outage must write at most one redis.unavailable event per window, wrote %d", added)
	}

	// Redis comes BACK: without a restart, the worker gets its consumer group created
	proxy.start(t, target)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if groups, err := real.XInfoGroups(context.Background(), cache.TaskStream).Result(); err == nil {
			for _, g := range groups {
				if g.Name == WorkerGroupA {
					return
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the worker never recovered after Redis came back")
}

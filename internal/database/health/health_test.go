package health

// RFC-005 §7 Component Health Projection.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/oklog/ulid/v2"
	"github.com/redis/go-redis/v9"

	"MTL_Scheduler_PII_Test/internal/cache"
	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
	"MTL_Scheduler_PII_Test/internal/pii"
)

func TestMain(m *testing.M) {
	godotenv.Load("../../../.env")
	database.ConnectDatabase()
	cache.ConnectRedis()
	database.DB.AutoMigrate(&models.Worker{}, &models.WorkerHeartbeat{})
	os.Exit(m.Run())
}

func TestComponentHealthOf_HealthAndEvidence(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	mk := func(ago time.Duration) models.ComponentInstance {
		return models.ComponentInstance{ComponentType: "Worker", InstanceID: "i", DisplayName: "w", BuildRevision: "r1", StartedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-ago)}
	}

	for name, tt := range map[string]struct {
		ago  time.Duration
		want string
	}{"healthy": {10 * time.Second, "HEALTHY"}, "degraded": {2 * time.Minute, "DEGRADED"}, "offline": {10 * time.Minute, "OFFLINE"}} {
		t.Run(name, func(t *testing.T) {
			h := ComponentHealthOf(mk(tt.ago), now, models.HeartbeatDegradedAfter, models.HeartbeatOfflineAfter)
			if h.Health != tt.want || h.BuildRevision != "r1" || h.LastHeartbeat == nil {
				t.Fatalf("%+v", h)
			}
			if h.Evidence["last_seen_age_seconds"] != int(tt.ago.Seconds()) || h.Evidence["degraded_after_seconds"] != 60 || h.Evidence["offline_after_seconds"] != 300 {
				t.Fatalf("the evidence must hold the numbers the verdict used: %v", h.Evidence)
			}
		})
	}

	never := ComponentHealthOf(models.ComponentInstance{ComponentType: "Worker", InstanceID: "i"}, now, models.HeartbeatDegradedAfter, models.HeartbeatOfflineAfter)
	if never.Health != "UNKNOWN" || never.LastHeartbeat != nil {
		t.Fatalf("an instance that never reported is UNKNOWN with no last heartbeat: %+v", never)
	}
	if _, has := never.Evidence["last_seen_age_seconds"]; has {
		t.Fatal("no age can be reported for a heartbeat that never happened")
	}
}

func seedWorker(t *testing.T, revision string, heartbeatAgo *time.Duration) string {
	t.Helper()
	instance := "health-" + ulid.Make().String()
	database.DB.Create(&models.Worker{WorkerId: "w-" + instance, InstanceId: instance, ComponentType: "Worker", Hostname: "h", StartedAt: time.Now().UTC().Add(-time.Hour), BuildRevision: revision})
	if heartbeatAgo != nil {
		database.DB.Create(&models.WorkerHeartbeat{WorkerId: "w-" + instance, InstanceId: instance, OccurredAt: time.Now().UTC().Add(-*heartbeatAgo), RunningAttempts: 1, Capacity: 4})
	}
	t.Cleanup(func() {
		database.DB.Unscoped().Where("instance_id = ?", instance).Delete(&models.Worker{})
		database.DB.Unscoped().Where("instance_id = ?", instance).Delete(&models.WorkerHeartbeat{})
	})
	return instance
}

func TestBuild_ReportsEachInstanceWithItsRevisionHealthAndEvidence(t *testing.T) {
	d := func(x time.Duration) *time.Duration { return &x }
	healthy := seedWorker(t, "rev-healthy", d(5*time.Second))
	degraded := seedWorker(t, "rev-degraded", d(2*time.Minute))
	offline := seedWorker(t, "rev-offline", d(20*time.Minute))
	never := seedWorker(t, "rev-never", nil)

	report, err := Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	byID := map[string]ComponentHealth{}
	for _, c := range report.Components {
		byID[c.InstanceID] = c
	}
	for id, want := range map[string][2]string{
		healthy: {"HEALTHY", "rev-healthy"}, degraded: {"DEGRADED", "rev-degraded"}, offline: {"OFFLINE", "rev-offline"}, never: {"UNKNOWN", "rev-never"},
	} {
		got, ok := byID[id]
		if !ok {
			t.Fatalf("instance %s is missing from the report", id)
		}
		if got.Health != want[0] || got.BuildRevision != want[1] || got.ComponentType != "Worker" {
			t.Errorf("instance %s: health=%s revision=%s, want %v", id, got.Health, got.BuildRevision, want)
		}
	}
	if report.GeneratedAt.IsZero() || len(report.Dependencies) != 3 {
		t.Fatalf("the report carries a timestamp and the dependency summary: %+v", report)
	}
}

func TestBuild_ObservedByCarriesTheBuildRevisionOfTheServingProcess(t *testing.T) {
	t.Setenv("BUILD_REVISION", "serving-rev-1")
	report, _ := Build(context.Background())
	if report.ObservedBy.BuildRevision != "serving-rev-1" {
		t.Fatalf("got %q", report.ObservedBy.BuildRevision)
	}
}

func dep(deps []DependencyStatus, name string) DependencyStatus {
	for _, d := range deps {
		if d.Name == name {
			return d
		}
	}
	return DependencyStatus{}
}

func TestCheckDependencies_ReportsEachDependencyAsObserved(t *testing.T) {
	var p models.PIIPolicy
	p.Metadata.Name, p.Metadata.Checksum = "default", "abc"
	prev := pii.LoadedPolicy.Load()
	pii.LoadedPolicy.Store(&p)
	t.Cleanup(func() { pii.LoadedPolicy.Store(prev) })

	deps := CheckDependencies(context.Background())
	for _, name := range []string{"postgres", "redis", "pii_policy"} {
		if d := dep(deps, name); d.Status != "OK" {
			t.Errorf("%s should be OK against the real services, got %+v", name, d)
		}
	}
}

func TestCheckDependencies_ADownRedisIsUnavailableAndThePostgresStillOKAndNoErrorLeaks(t *testing.T) {
	real := cache.Client
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 300 * time.Millisecond, MaxRetries: -1})
	cache.Client = dead
	t.Cleanup(func() { cache.Client = real; dead.Close() })

	deps := CheckDependencies(context.Background())

	if d := dep(deps, "redis"); d.Status != "UNAVAILABLE" || d.Detail != "ping failed" {
		t.Fatalf("redis: %+v", d)
	}
	if d := dep(deps, "postgres"); d.Status != "OK" {
		t.Fatalf("one dependency being down must not hide the others: %+v", d)
	}
	if d := dep(deps, "redis"); d.Detail != "ping failed" {
		t.Fatal("the detail must be generic: error text carries hostnames, users and database names")
	}
}

func TestCheckDependencies_NoActivePolicyIsUnavailable(t *testing.T) {
	prev := pii.LoadedPolicy.Load()
	pii.LoadedPolicy.Store(&models.PIIPolicy{})
	t.Cleanup(func() { pii.LoadedPolicy.Store(prev) })

	if d := dep(CheckDependencies(context.Background()), "pii_policy"); d.Status != "UNAVAILABLE" {
		t.Fatalf("a policy with no checksum is not an active policy: %+v", d)
	}
}

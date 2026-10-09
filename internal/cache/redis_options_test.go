package cache

// RFC-003 §14: "Redis access must be restricted." The app has to be able to log in to a Redis
// that has AUTH turned on.

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestRedisOptions_CarryUsernamePasswordAndTLS(t *testing.T) {
	opts, warns := redisOptionsFromEnv(envFrom(map[string]string{
		"REDIS_ADDR": "redis.internal:6379", "REDIS_USERNAME": "scheduler", "REDIS_PASSWORD": "s3cret", "REDIS_TLS": "true",
	}))

	if opts.Addr != "redis.internal:6379" || opts.Username != "scheduler" || opts.Password != "s3cret" {
		t.Fatalf("credentials were not carried through: %+v", opts)
	}
	if opts.TLSConfig == nil {
		t.Fatal("REDIS_TLS=true must enable TLS")
	}
	if len(warns) != 0 {
		t.Fatalf("a remote host WITH a password should not warn: %v", warns)
	}
}

func TestRedisOptions_TLSIsOffByDefault(t *testing.T) {
	opts, _ := redisOptionsFromEnv(envFrom(map[string]string{"REDIS_ADDR": "localhost:6379"}))
	if opts.TLSConfig != nil {
		t.Fatal("TLS must not turn itself on")
	}
}

func TestRedisOptions_WarnsOnlyWhenARemoteRedisHasNoPassword(t *testing.T) {
	tests := []struct {
		name string
		addr string
		pass string
		warn bool
	}{
		{"remote, no password", "redis.example.com:6379", "", true},
		{"remote ip, no password", "10.0.0.5:6379", "", true},
		{"remote, password set", "redis.example.com:6379", "x", false},
		{"localhost, no password", "localhost:6379", "", false},
		{"127.0.0.1, no password", "127.0.0.1:6379", "", false},
		{"ipv6 loopback, no password", "[::1]:6379", "", false},
		{"unset address means localhost", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, warns := redisOptionsFromEnv(envFrom(map[string]string{"REDIS_ADDR": tt.addr, "REDIS_PASSWORD": tt.pass}))
			if (len(warns) > 0) != tt.warn {
				t.Fatalf("warnings = %v, want a warning: %v", warns, tt.warn)
			}
		})
	}
}

func TestRedisOptions_TheWarningNeverContainsThePassword(t *testing.T) {
	_, warns := redisOptionsFromEnv(envFrom(map[string]string{"REDIS_ADDR": "redis.example.com:6379"}))
	for _, w := range warns {
		if strings.Contains(strings.ToLower(w), "s3cret") {
			t.Fatal("a warning leaked a password")
		}
	}
}

// ---------------------------------------------------------------------------
// A real Redis that demands a password. Skipped where redis-server is not installed.
// ---------------------------------------------------------------------------

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestRedisOptions_ConnectToARealRedisThatRequiresAPassword(t *testing.T) {
	bin, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed here, skipping the live AUTH check")
	}

	port := freePort(t)
	cmd := exec.Command(bin, "--port", fmt.Sprint(port), "--bind", "127.0.0.1", "--requirepass", "test-password", "--save", "", "--appendonly", "no")
	if err := cmd.Start(); err != nil {
		t.Skipf("could not start redis-server: %v", err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// wait until it is accepting connections
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			c.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	withPassword, _ := redisOptionsFromEnv(envFrom(map[string]string{"REDIS_ADDR": addr, "REDIS_PASSWORD": "test-password"}))
	good := redis.NewClient(withPassword)
	defer good.Close()
	if err := good.Ping(ctx).Err(); err != nil {
		t.Fatalf("the app could not log in with the right password: %v", err)
	}

	noPassword, _ := redisOptionsFromEnv(envFrom(map[string]string{"REDIS_ADDR": addr}))
	bad := redis.NewClient(noPassword)
	defer bad.Close()
	err = bad.Ping(ctx).Err()
	if err == nil || !strings.Contains(strings.ToUpper(err.Error()), "NOAUTH") && !strings.Contains(strings.ToUpper(err.Error()), "AUTH") {
		t.Fatalf("a client without the password must be refused, got: %v", err)
	}
}

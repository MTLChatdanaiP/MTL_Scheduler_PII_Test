package cache

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

var Ctx = context.Background()

var Client *redis.Client

// RFC-003 §5 Delivery Envelope: stream name is part of the delivery envelope/coordination substrate
const (
	TaskStream = "tasks:stream"
)

// isLoopbackAddr takes a host:port and returns true if it points at this machine. An empty
// address counts, because go-redis then defaults to localhost.
func isLoopbackAddr(addr string) bool {
	if strings.TrimSpace(addr) == "" {
		return true
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// redisOptionsFromEnv takes a lookup function (so it can be tested) and returns the
// connection options plus any warnings, by reading REDIS_ADDR, REDIS_USERNAME,
// REDIS_PASSWORD and REDIS_TLS.
//
// RFC-003 §14: "Redis access must be restricted." Turning AUTH on is something only the
// Redis operator can do, but the app has to be able to log in once it is on. Before this
// the client was built from the address alone, so enabling AUTH would have broken it.
func redisOptionsFromEnv(getenv func(string) string) (*redis.Options, []string) {
	opts := &redis.Options{
		Addr:     getenv("REDIS_ADDR"),
		Username: getenv("REDIS_USERNAME"),
		Password: getenv("REDIS_PASSWORD"),
	}

	if strings.EqualFold(strings.TrimSpace(getenv("REDIS_TLS")), "true") {
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	var warnings []string
	if opts.Password == "" && !isLoopbackAddr(opts.Addr) {
		warnings = append(warnings, "REDIS_ADDR points at a non-loopback host but REDIS_PASSWORD is not set: anyone who can reach that address can read and write the task stream")
	}

	return opts, warnings
}

// RFC-003 §1 Summary: "Redis is a runtime delivery substrate. It is not the durable source of historical monitoring truth."
func ConnectRedis() {

	godotenv.Load()

	opts, warnings := redisOptionsFromEnv(os.Getenv)
	for _, w := range warnings {
		slog.Warn(w) // never logs the password itself
	}

	Client = redis.NewClient(opts)

	_, err := Client.Ping(Ctx).Result()

	if err != nil {
		panic(err)
	}

	fmt.Println("Connected to Redis")
}

package cache

import (
	"context"
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

var Ctx = context.Background()

var Client *redis.Client

// RFC-003 §5 Delivery Envelope: stream name is part of the delivery envelope/coordination substrate
const (
	TaskStream = "tasks:stream"
)

// RFC-003 §1 Summary: "Redis is a runtime delivery substrate. It is not the durable source of historical monitoring truth."
func ConnectRedis() {

	godotenv.Load()

	Client = redis.NewClient(&redis.Options{
		Addr: os.Getenv("REDIS_ADDR"),
	})

	_, err := Client.Ping(Ctx).Result()

	if err != nil {
		panic(err)
	}

	fmt.Println("Connected to Redis")
}

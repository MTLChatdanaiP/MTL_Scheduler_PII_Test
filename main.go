package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	redisdb "MTL_Scheduler_PII_Test/internals/cache"
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
	"MTL_Scheduler_PII_Test/internals/routes"
	"MTL_Scheduler_PII_Test/internals/shutdown"
	"MTL_Scheduler_PII_Test/internals/worker"
)

const stream = "jobs:experiment"

func main() {
	godotenv.Load()

	database.ConnectDatabase()
	redisdb.ConnectRedis()

	database.DB.AutoMigrate(
		&models.Task{}, &models.PIIRecord{}, &models.EventEnvelope{}, &models.RunProjection{}, &models.Worker{}, &models.WorkerHeartbeat{}, &models.QueueHealth{}, &models.Attempt{}, &models.ExecutionChain{})

	r := routes.SetupRouter()

	srv := &http.Server{
		Addr:    ":8080",
		Handler: r, // your gin.Engine — think about why Gin's Engine can be used as a Handler here (hint: what interface must a type satisfy to be usable as http.Server's Handler?)
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("server error: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup

	wg.Add(4)

	go func() {
		defer wg.Done()
		worker.SetupWorker(ctx, "Consumer-a")
	}()

	go func() {
		defer wg.Done()
		worker.StartReclaimer(ctx, "Consumer_Backup")
	}()

	go func() {
		defer wg.Done()
		worker.StartScheduler(ctx)
	}()

	go func() {
		defer wg.Done()
		worker.StartQueueHealth(ctx)
	}()

	fmt.Println("Running... press Ctrl+C to stop")
	<-ctx.Done()

	shutdown.Run(context.Background(), []shutdown.Phase{
		{
			Name:    "http-server",
			Timeout: 10 * time.Second,
			Run: func(ctx context.Context) error {
				// RFC-004 §10 Graceful Shutdown: stop accepting new HTTP
				// requests first, so no new tasks enter the system while
				// the rest of shutdown proceeds.
				return srv.Shutdown(ctx)
			},
		},
		{
			Name:    "background-workers",
			Timeout: 40 * time.Second, // comfortably longer than ProcessTask's 30s sleep
			Run: func(ctx context.Context) error {
				// Worker/reclaimer/scheduler were already told to stop via
				// the earlier ctx.Done() — this phase just waits (bounded)
				// for them to actually finish any in-flight work.
				return shutdown.WaitGroup(ctx, &wg)
			},
		},
		{
			Name:    "infra-connections",
			Timeout: 5 * time.Second,
			Run: func(ctx context.Context) error {
				if sqlDB, err := database.DB.DB(); err == nil {
					sqlDB.Close()
				}
				return redisdb.Client.Close()
			},
		},
	})

	fmt.Println("Shutdown complete, exiting")
}

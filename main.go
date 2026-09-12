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

	alerts "MTL_Scheduler_PII_Test/internals/alerting"
	"MTL_Scheduler_PII_Test/internals/auth"
	redisdb "MTL_Scheduler_PII_Test/internals/cache"
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/models"
	pii "MTL_Scheduler_PII_Test/internals/pii"
	"MTL_Scheduler_PII_Test/internals/routes"
	"MTL_Scheduler_PII_Test/internals/shutdown"
	"MTL_Scheduler_PII_Test/internals/worker"
)

func main() {
	godotenv.Load()

	database.ConnectDatabase()
	redisdb.ConnectRedis()

	if _, err := pii.ActivatePolicy(context.Background(), "policies/default.json", "STARTUP", "system"); err != nil {
		log.Fatal("Broken Policy, Stopping App", err)
	}

	if _, err := alerts.ActivateRules(context.Background(), "internals/alerting/rules.json", "system"); err != nil {
		log.Fatal("Broken Alerts Rules, Stopping App", err)
	}

	if err := auth.LoadPrincipals("internals/auth/config.json"); err != nil {
		log.Fatal(err)
	}

	database.DB.AutoMigrate(
		&models.Task{}, &models.PIIRecord{},
		&models.EventEnvelope{}, &models.RunProjection{},
		&models.Worker{}, &models.WorkerHeartbeat{}, &models.QueueHealth{},
		&models.Attempt{}, &models.ExecutionChain{}, &models.ScheduleDefinition{},
		&models.MonitoringAnnotation{}, &models.MonitoringHealth{}, &models.PIIVault{}, &models.PolicyActivation{},
		&models.Alert{}, &models.Notification{},
	)

	r := routes.SetupRouter()

	srv := &http.Server{
		Addr:    ":8080",
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("server error: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if url := os.Getenv("ALERT_WEBHOOK_URL"); url != "" {
		adapter := alerts.NewWebhookAdapter(url)
		alerts.NotificationAdapters[adapter.Channel()] = adapter
	}

	var wg sync.WaitGroup

	wg.Add(6)

	go func() { defer wg.Done(); worker.SetupWorker(ctx, "Consumer-a") }()
	go func() { defer wg.Done(); worker.StartReclaimer(ctx, "Consumer_Backup") }()
	go func() { defer wg.Done(); worker.StartScheduler(ctx, "Schedule_Buddy") }()
	go func() { defer wg.Done(); worker.StartQueueHealth(ctx) }()
	go func() { defer wg.Done(); worker.StartMonitoringSweep(ctx) }()
	go func() { defer wg.Done(); alerts.StartAlertsSweep(ctx) }()

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

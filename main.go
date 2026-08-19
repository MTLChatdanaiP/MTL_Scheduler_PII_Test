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
	"MTL_Scheduler_PII_Test/internals/worker"
)

const stream = "jobs:experiment"

func main() {
	godotenv.Load()

	database.ConnectDatabase()
	redisdb.ConnectRedis()

	database.DB.AutoMigrate(
		&models.Task{}, &models.PIIRecord{}, &models.EventEnvelope{}, &models.RunProjection{}, &models.Worker{}, &models.WorkerHeartbeat{})

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

	wg.Add(3)

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

	fmt.Println("Running... press Ctrl+C to stop")
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	wg.Wait()
	fmt.Println("Shutdown signal received, exiting")
}

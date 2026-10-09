package events

// The events package had no tests. It owns the single writer of event_envelopes (LogEvent), so it
// gets the same real-database setup the worker and taskservice packages use.

import (
	"os"
	"testing"

	"github.com/joho/godotenv"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/models"
)

func TestMain(m *testing.M) {
	godotenv.Load("../../.env")
	database.ConnectDatabase()

	database.DB.AutoMigrate(
		&models.Task{}, &models.EventEnvelope{}, &models.RunProjection{}, &models.Attempt{}, &models.ExecutionChain{},
		&models.PIIRecord{}, &models.MonitoringAnnotation{}, &models.Alert{},
		&models.ScheduleOccurrence{}, &models.ScheduleProjection{},
	)

	os.Exit(m.Run())
}

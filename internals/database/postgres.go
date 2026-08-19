package database

import (
	"fmt"
	"os"

	"MTL_Scheduler_PII_Test/internals/models"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

// PRD §50 Data Persistence / RFC-005 §17 Persistence: "Redis is not sufficient for durable monitoring history. A durable database should retain..."
func ConnectDatabase() {

	err := godotenv.Load()

	if err != nil {
		fmt.Println(".env not found")
	}

	dsn := fmt.Sprintf(
		"host=%s user=%s password=%s dbname=%s port=%s sslmode=disable",
		os.Getenv("DB_HOST"),
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_NAME"),
		os.Getenv("DB_PORT"),
	)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})

	if err != nil {
		panic(err)
	}

	DB = db

	DB.AutoMigrate(&models.Task{})

	fmt.Println("Connected to PostgreSQL")
}

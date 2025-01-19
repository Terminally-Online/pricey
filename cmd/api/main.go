package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"

	"pricey/internal/api"
	"pricey/internal/database"
	"pricey/internal/service/historical"
	"pricey/internal/service/historical/types"
	"pricey/internal/service/uniswap"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: .env file not found: %v", err)
	}

	// Connect to Ethereum node
	ethClient, err := ethclient.Dial(os.Getenv("ETH_NODE_URL"))
	if err != nil {
		log.Fatalf("Failed to connect to Ethereum node: %v", err)
	}

	// Initialize database
	dbPort, _ := strconv.Atoi(os.Getenv("DB_PORT"))
	if dbPort == 0 {
		dbPort = 5432 // default PostgreSQL port
	}

	db, err := database.New(database.Config{
		Host:     os.Getenv("DB_HOST"),
		Port:     dbPort,
		User:     os.Getenv("DB_USER"),
		Password: os.Getenv("DB_PASSWORD"),
		DBName:   os.Getenv("DB_NAME"),
		SSLMode:  os.Getenv("DB_SSL_MODE"),
	})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Run database migrations
	if err := db.RunMigrations(context.Background()); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Initialize services
	priceService := uniswap.NewService(ethClient)

	// Create historical client with rate limiting
	histClient, err := historical.NewClient(ethClient, types.DefaultConfig().RateLimit)
	if err != nil {
		log.Fatalf("Failed to create historical client: %v", err)
	}
	defer histClient.Close()

	// Create backfill manager
	config := types.DefaultConfig()
	backfillManager := historical.NewBackfillManager(db, histClient, config)

	// Get API port from environment or use default
	apiPort, _ := strconv.Atoi(os.Getenv("API_PORT"))
	if apiPort == 0 {
		apiPort = 8080
	}

	// Create API service
	apiService := api.NewService(priceService, db, apiPort)

	// Create a context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start backfill process in background
	go func() {
		log.Printf("Starting backfill process...")
		if err := backfillManager.Run(ctx); err != nil && err != context.Canceled {
			log.Printf("Backfill process error: %v", err)
		}
	}()

	// Start API server
	go func() {
		log.Printf("Starting API server on :%d", apiPort)
		if err := apiService.Start(); err != nil && err != http.ErrServerClosed {
			log.Printf("API server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	// Graceful shutdown
	log.Printf("Shutting down services...")

	// Cancel context to stop backfill process
	cancel()

	// Stop API server
	if err := apiService.Stop(context.Background()); err != nil {
		log.Printf("Error during API server shutdown: %v", err)
	}
}

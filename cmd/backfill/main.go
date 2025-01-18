package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"

	"pricey/internal/database"
	"pricey/internal/service/historical"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: .env file not found: %v", err)
	}

	// Connect to Ethereum node
	ethURL := os.Getenv("ETH_NODE_URL")
	if ethURL == "" {
		log.Fatal("ETH_NODE_URL environment variable is required")
	}
	ethClient, err := ethclient.Dial(ethURL)
	if err != nil {
		log.Fatalf("Failed to connect to Ethereum node: %v", err)
	}

	// Connect to database
	db, err := database.New(database.Config{
		Host:     os.Getenv("DB_HOST"),
		Port:     5432, // TODO: make configurable
		User:     os.Getenv("DB_USER"),
		Password: os.Getenv("DB_PASSWORD"),
		DBName:   os.Getenv("DB_NAME"),
		SSLMode:  os.Getenv("DB_SSL_MODE"),
	})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Run migrations
	if err := db.RunMigrations(context.Background()); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Create historical client with rate limiting
	histClient, err := historical.NewClient(ethClient, historical.DefaultConfig().RateLimit)
	if err != nil {
		log.Fatalf("Failed to create historical client: %v", err)
	}
	defer histClient.Close()

	// Create and run backfill manager
	config := historical.DefaultConfig()
	manager := historical.NewBackfillManager(db, histClient, config)

	// Set up graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		fmt.Printf("\nReceived signal %v, shutting down...\n", sig)
		cancel()
	}()

	// Run the backfill process
	if err := manager.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("Backfill process failed: %v", err)
	}
}

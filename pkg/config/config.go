package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	EthereumNodeURL string
}

// Load reads the config from environment variables, optionally loading from .env file
func Load() (*Config, error) {
	// Try to load .env from project root
	projectRoot := findProjectRoot()
	err := godotenv.Load(filepath.Join(projectRoot, ".env"))
	if err != nil {
		return nil, fmt.Errorf("failed to load .env file: %w", err)
	}

	cfg := &Config{}

	// Required variables
	nodeURL := os.Getenv("ETHEREUM_NODE_URL")
	if nodeURL == "" {
		return nil, fmt.Errorf("ETHEREUM_NODE_URL environment variable is required")
	}
	cfg.EthereumNodeURL = strings.TrimSpace(nodeURL)

	return cfg, nil
}

// findProjectRoot looks for common project root indicators
func findProjectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

#!/bin/bash
set -e

# Load environment variables
if [ -f .env ]; then
    export $(cat .env | grep -v '^#' | xargs)
fi

# Ensure database is running
if ! docker ps | grep -q timescaledb; then
    echo "Starting TimescaleDB..."
    docker-compose up -d
    sleep 5  # Wait for database to be ready
fi

# Build and run the API
echo "Building API server..."
go build -o bin/pricey-api ./cmd/api

echo "Starting API server..."
./bin/pricey-api 
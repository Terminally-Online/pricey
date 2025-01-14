package database

import (
	"context"
	_ "embed" // needed for embedding files
	"fmt"
)

//go:embed migrations/001_initial_schema.sql
var initialSchema string

// RunMigrations runs all database migrations
func (db *DB) RunMigrations(ctx context.Context) error {
	// Run initial schema migration
	if _, err := db.pool.Exec(ctx, initialSchema); err != nil {
		return fmt.Errorf("error running initial schema migration: %w", err)
	}

	return nil
}

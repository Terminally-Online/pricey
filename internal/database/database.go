package database

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/jackc/pgx"
	"github.com/jackc/pgx/v4/pgxpool"
)

// Config holds database configuration
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string
}

// DB wraps the database connection pool
type DB struct {
	pool *pgxpool.Pool
}

// New creates a new database connection
func New(cfg Config) (*DB, error) {
	connStr := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.DBName, cfg.SSLMode,
	)

	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("error parsing config: %w", err)
	}

	// Set connection pool settings
	config.MaxConns = 25
	config.MinConns = 5
	config.MaxConnLifetime = 5 * time.Minute

	// Create connection pool
	pool, err := pgxpool.ConnectConfig(context.Background(), config)
	if err != nil {
		return nil, fmt.Errorf("error connecting to the database: %w", err)
	}

	return &DB{pool: pool}, nil
}

// Close closes the database connection pool
func (db *DB) Close() {
	if db.pool != nil {
		db.pool.Close()
	}
}

// InsertToken inserts a new token into the database
func (db *DB) InsertToken(ctx context.Context, address []byte, symbol string, decimals int, tokenType string) error {
	query := `
		INSERT INTO tokens (address, symbol, decimals, token_type)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (address) DO UPDATE
		SET symbol = $2, decimals = $3, token_type = $4, updated_at = NOW()
	`

	_, err := db.pool.Exec(ctx, query, address, symbol, decimals, tokenType)
	if err != nil {
		return fmt.Errorf("error inserting token: %w", err)
	}

	return nil
}

// InsertPair inserts a new pair into the database
func (db *DB) InsertPair(ctx context.Context, address, token0, token1 []byte, createdAtBlock int64) error {
	query := `
		INSERT INTO pairs (address, token0_address, token1_address, created_at_block)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (address) DO UPDATE
		SET token0_address = $2, token1_address = $3, created_at_block = $4, updated_at = NOW()
	`

	_, err := db.pool.Exec(ctx, query, address, token0, token1, createdAtBlock)
	if err != nil {
		return fmt.Errorf("error inserting pair: %w", err)
	}

	return nil
}

// InsertPairPrice inserts a new price record for a pair
func (db *DB) InsertPairPrice(ctx context.Context, pairAddress []byte, blockNumber int64, blockTime time.Time,
	reserves0, reserves1, price0USD, price1USD, tvlUSD, confidence float64) error {

	query := `
		INSERT INTO pair_prices (
			pair_address, block_number, block_time, reserves0, reserves1,
			price0_usd, price1_usd, tvl_usd, confidence
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (pair_address, block_number, block_time) DO UPDATE
		SET 
			reserves0 = $4,
			reserves1 = $5,
			price0_usd = $6,
			price1_usd = $7,
			tvl_usd = $8,
			confidence = $9
	`

	_, err := db.pool.Exec(ctx, query, pairAddress, blockNumber, blockTime,
		reserves0, reserves1, price0USD, price1USD, tvlUSD, confidence)
	if err != nil {
		return fmt.Errorf("error inserting pair price: %w", err)
	}

	return nil
}

// GetLatestPairPrice gets the most recent price record for a pair
func (db *DB) GetLatestPairPrice(ctx context.Context, pairAddress []byte) (blockNumber int64, blockTime time.Time,
	reserves0, reserves1, price0USD, price1USD, tvlUSD, confidence float64, err error) {

	query := `
		SELECT block_number, block_time, reserves0, reserves1,
			   price0_usd, price1_usd, tvl_usd, confidence
		FROM latest_pair_prices
		WHERE pair_address = $1
	`

	err = db.pool.QueryRow(ctx, query, pairAddress).Scan(
		&blockNumber, &blockTime, &reserves0, &reserves1,
		&price0USD, &price1USD, &tvlUSD, &confidence,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, time.Time{}, 0, 0, 0, 0, 0, 0, nil
		}
		return 0, time.Time{}, 0, 0, 0, 0, 0, 0, fmt.Errorf("error getting latest pair price: %w", err)
	}

	return
}

// UpdatePairProgress updates the last processed block for a pair
func (db *DB) UpdatePairProgress(ctx context.Context, pairAddress []byte, lastProcessedBlock uint64) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO pair_sync_progress (pair_address, last_processed_block, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (pair_address) 
		DO UPDATE SET 
			last_processed_block = $2,
			updated_at = NOW()
	`, pairAddress, lastProcessedBlock)
	return err
}

// GetPairProgress gets the last processed block for a pair
func (db *DB) GetPairProgress(ctx context.Context, pairAddress []byte) (uint64, error) {
	var lastBlock uint64
	err := db.pool.QueryRow(ctx, `
		SELECT last_processed_block 
		FROM pair_sync_progress 
		WHERE pair_address = $1
	`, pairAddress).Scan(&lastBlock)
	if err != nil {
		return 0, fmt.Errorf("error getting pair progress: %w", err)
	}
	return lastBlock, nil
}

// GetPairsForBackfill gets pairs that need historical data collection
func (db *DB) GetPairsForBackfill(ctx context.Context, limit int) ([]common.Address, error) {
	query := `
		WITH pair_blocks AS (
			SELECT p.address, p.created_at_block,
				   COALESCE(sp.last_processed_block, p.created_at_block) as last_processed,
				   (SELECT MAX(block_number) FROM pair_prices WHERE pair_address = p.address) as max_processed_block
			FROM pairs p
			LEFT JOIN pair_sync_progress sp ON p.address = sp.pair_address
		)
		SELECT encode(address, 'hex')
		FROM pair_blocks
		WHERE last_processed > 0
		ORDER BY (max_processed_block - last_processed) DESC NULLS FIRST,
		         last_processed DESC
		LIMIT $1
	`

	rows, err := db.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("error querying pairs for backfill: %w", err)
	}
	defer rows.Close()

	var pairs []common.Address
	for rows.Next() {
		var addrHex string
		if err := rows.Scan(&addrHex); err != nil {
			return nil, fmt.Errorf("error scanning pair address: %w", err)
		}
		pairs = append(pairs, common.HexToAddress(addrHex))
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating pairs: %w", err)
	}

	return pairs, nil
}

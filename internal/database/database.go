package database

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"time"

	histtypes "pricey/internal/service/historical/types"

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
func (db *DB) InsertToken(ctx context.Context, address []byte, name string, symbol string, decimals int, tokenType string) error {
	query := `
		INSERT INTO tokens (address, name, symbol, decimals, token_type)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (address) DO UPDATE
		SET name = $2, symbol = $3, decimals = $4, token_type = $5, updated_at = NOW()
	`

	_, err := db.pool.Exec(ctx, query, address, name, symbol, decimals, tokenType)
	if err != nil {
		return fmt.Errorf("error inserting token: %w", err)
	}

	return nil
}

// InsertPair inserts a new pair into the database
func (db *DB) InsertPair(ctx context.Context, address, token0, token1 []byte, createdAtBlock int64) error {
	// Check if both tokens exist first
	checkQuery := `
		SELECT COUNT(*)
		FROM tokens
		WHERE address IN ($1, $2)
	`
	var count int
	err := db.pool.QueryRow(ctx, checkQuery, token0, token1).Scan(&count)
	if err != nil {
		return fmt.Errorf("error checking token existence: %w", err)
	}
	if count != 2 {
		return fmt.Errorf("one or both tokens do not exist in database")
	}

	// Ensure token0 < token1 to match the constraint
	if bytes.Compare(token0, token1) > 0 {
		token0, token1 = token1, token0
	}

	query := `
		INSERT INTO pairs (address, token0_address, token1_address, created_at_block)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (address) DO UPDATE
		SET token0_address = $2,
			token1_address = $3,
			created_at_block = $4,
			updated_at = NOW()
	`

	_, err = db.pool.Exec(ctx, query, address, token0, token1, createdAtBlock)
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
	query := `
		INSERT INTO pair_sync_progress (pair_address, last_processed_block, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (pair_address) DO UPDATE
		SET last_processed_block = $2,
			updated_at = NOW()
	`

	_, err := db.pool.Exec(ctx, query, pairAddress, lastProcessedBlock)
	if err != nil {
		return fmt.Errorf("error updating pair progress: %w", err)
	}

	return nil
}

// GetPairProgress gets the last processed block for a pair
func (db *DB) GetPairProgress(ctx context.Context, pairAddress []byte) (uint64, error) {
	var lastProcessedBlock uint64
	err := db.pool.QueryRow(ctx, `
		SELECT last_processed_block
		FROM pair_sync_progress
		WHERE pair_address = $1
	`, pairAddress).Scan(&lastProcessedBlock)

	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, fmt.Errorf("no rows in result set")
		}
		return 0, fmt.Errorf("error getting pair progress: %w", err)
	}

	return lastProcessedBlock, nil
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

// GetActiveTokens returns all tokens, with price data if available
func (db *DB) GetActiveTokens(ctx context.Context) ([]struct {
	Address    string
	Name       string
	Symbol     string
	Decimals   int
	PriceUSD   sql.NullFloat64
	Confidence sql.NullFloat64
	UpdatedAt  time.Time
}, error) {
	query := `
		WITH latest_prices AS (
			SELECT DISTINCT ON (pair_address)
				pair_address,
				price0_usd,
				price1_usd,
				confidence,
				block_time as updated_at
			FROM pair_prices
			ORDER BY pair_address, block_time DESC
		),
		token_prices AS (
			-- Get prices where token is token0
			SELECT 
				t.address,
				t.name,
				t.symbol,
				t.decimals,
				lp.price0_usd as price,
				lp.confidence,
				lp.updated_at
			FROM tokens t
			LEFT JOIN pairs p ON t.address = p.token0_address
			LEFT JOIN latest_prices lp ON p.address = lp.pair_address
			WHERE lp.price0_usd IS NOT NULL
			
			UNION ALL
			
			-- Get prices where token is token1
			SELECT 
				t.address,
				t.name,
				t.symbol,
				t.decimals,
				lp.price1_usd as price,
				lp.confidence,
				lp.updated_at
			FROM tokens t
			LEFT JOIN pairs p ON t.address = p.token1_address
			LEFT JOIN latest_prices lp ON p.address = lp.pair_address
			WHERE lp.price1_usd IS NOT NULL
		),
		best_prices AS (
			-- Select the best price for each token based on confidence
			SELECT DISTINCT ON (address)
				address,
				name,
				symbol,
				decimals,
				price,
				confidence,
				updated_at
			FROM token_prices
			ORDER BY address, confidence DESC, updated_at DESC
		)
		-- Final selection including tokens without prices
		SELECT 
			encode(t.address, 'hex') as address,
			COALESCE(t.name, t.symbol) as name,
			t.symbol,
			t.decimals,
			bp.price as price_usd,
			bp.confidence,
			COALESCE(bp.updated_at, t.updated_at) as updated_at
		FROM tokens t
		LEFT JOIN best_prices bp ON t.address = bp.address
		ORDER BY COALESCE(bp.updated_at, t.updated_at) DESC;
	`

	rows, err := db.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("error querying active tokens: %w", err)
	}
	defer rows.Close()

	var tokens []struct {
		Address    string
		Name       string
		Symbol     string
		Decimals   int
		PriceUSD   sql.NullFloat64
		Confidence sql.NullFloat64
		UpdatedAt  time.Time
	}

	for rows.Next() {
		var token struct {
			Address    string
			Name       string
			Symbol     string
			Decimals   int
			PriceUSD   sql.NullFloat64
			Confidence sql.NullFloat64
			UpdatedAt  time.Time
		}
		if err := rows.Scan(&token.Address, &token.Name, &token.Symbol, &token.Decimals, &token.PriceUSD, &token.Confidence, &token.UpdatedAt); err != nil {
			return nil, fmt.Errorf("error scanning token row: %w", err)
		}

		tokens = append(tokens, token)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tokens: %w", err)
	}

	return tokens, nil
}

// GetGlobalSyncProgress returns the last processed block from global sync
func (db *DB) GetGlobalSyncProgress(ctx context.Context) (uint64, error) {
	var lastBlock uint64
	err := db.pool.QueryRow(ctx, `
		SELECT last_processed_block 
		FROM global_sync_progress 
		ORDER BY id DESC 
		LIMIT 1
	`).Scan(&lastBlock)
	if err != nil {
		if err == pgx.ErrNoRows {
			return 0, nil
		}
		return 0, fmt.Errorf("error getting global sync progress: %w", err)
	}
	return lastBlock, nil
}

// UpdateGlobalSyncProgress updates the last processed block in global sync
func (db *DB) UpdateGlobalSyncProgress(ctx context.Context, lastProcessedBlock uint64) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO global_sync_progress (last_processed_block)
		VALUES ($1)
		ON CONFLICT (id) DO UPDATE
		SET last_processed_block = $1,
		    updated_at = NOW()
	`, lastProcessedBlock)
	if err != nil {
		return fmt.Errorf("error updating global sync progress: %w", err)
	}
	return nil
}

// RunMigrations runs all database migrations
func (db *DB) RunMigrations(ctx context.Context) error {
	migrations := []string{
		`
		CREATE TABLE IF NOT EXISTS tokens (
			address BYTEA PRIMARY KEY,
			name TEXT,
			symbol TEXT,
			decimals INTEGER NOT NULL,
			token_type VARCHAR(20) NOT NULL,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);
		`,
		`
		CREATE TABLE IF NOT EXISTS pairs (
			address BYTEA PRIMARY KEY,
			token0_address BYTEA NOT NULL REFERENCES tokens(address),
			token1_address BYTEA NOT NULL REFERENCES tokens(address),
			created_at_block BIGINT NOT NULL,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);
		`,
		`
		CREATE TABLE IF NOT EXISTS pair_prices (
			pair_address BYTEA NOT NULL REFERENCES pairs(address),
			block_number BIGINT NOT NULL,
			block_time TIMESTAMP WITH TIME ZONE NOT NULL,
			reserves0 DOUBLE PRECISION NOT NULL,
			reserves1 DOUBLE PRECISION NOT NULL,
			price0_usd DOUBLE PRECISION NOT NULL,
			price1_usd DOUBLE PRECISION NOT NULL,
			tvl_usd DOUBLE PRECISION NOT NULL,
			confidence DOUBLE PRECISION NOT NULL,
			PRIMARY KEY (pair_address, block_number, block_time)
		);
		`,
		`
		CREATE TABLE IF NOT EXISTS pair_sync_progress (
			pair_address BYTEA PRIMARY KEY REFERENCES pairs(address),
			last_processed_block BIGINT NOT NULL,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);
		`,
		`
		CREATE TABLE IF NOT EXISTS global_sync_progress (
			id SERIAL PRIMARY KEY,
			last_processed_block BIGINT NOT NULL,
			updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		);
		INSERT INTO global_sync_progress (id, last_processed_block)
		VALUES (1, 0)
		ON CONFLICT (id) DO NOTHING;
		`,
	}

	for _, migration := range migrations {
		if _, err := db.pool.Exec(ctx, migration); err != nil {
			return fmt.Errorf("error running migration: %w", err)
		}
	}

	return nil
}

// GetToken gets a token's metadata from the database
func (db *DB) GetToken(ctx context.Context, address []byte) (*histtypes.Token, error) {
	query := `
		SELECT symbol, name, decimals
		FROM tokens
		WHERE address = $1
	`

	var token histtypes.Token
	err := db.pool.QueryRow(ctx, query, address).Scan(&token.Symbol, &token.Name, &token.Decimals)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("token not found")
		}
		return nil, fmt.Errorf("error getting token: %w", err)
	}

	return &token, nil
}

// GetPair gets a pair by its address
func (db *DB) GetPair(ctx context.Context, pairAddress []byte) (*histtypes.Pair, error) {
	var pair histtypes.Pair
	err := db.pool.QueryRow(ctx, `
		SELECT address, token0_address, token1_address, created_at_block, created_at, updated_at
		FROM pairs
		WHERE address = $1
	`, pairAddress).Scan(
		&pair.Address,
		&pair.Token0Address,
		&pair.Token1Address,
		&pair.CreatedAtBlock,
		&pair.CreatedAt,
		&pair.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("pair not found")
		}
		return nil, fmt.Errorf("error getting pair: %w", err)
	}
	return &pair, nil
}

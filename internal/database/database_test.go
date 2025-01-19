package database

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) *DB {
	cfg := Config{
		Host:     "localhost",
		Port:     5432,
		User:     "pricey",
		Password: "pricey",
		DBName:   "pricey",
		SSLMode:  "disable",
	}

	db, err := New(cfg)
	require.NoError(t, err)
	require.NotNil(t, db)

	// Run migrations
	err = db.RunMigrations(context.Background())
	require.NoError(t, err)

	return db
}

func cleanupDB(t *testing.T, db *DB) {
	ctx := context.Background()
	_, err := db.pool.Exec(ctx, `
		TRUNCATE TABLE pair_sync_progress CASCADE;
		TRUNCATE TABLE pair_prices CASCADE;
		TRUNCATE TABLE pairs CASCADE;
		TRUNCATE TABLE tokens CASCADE;
	`)
	require.NoError(t, err)
}

func TestDatabaseConnection(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Test connection
	err := db.pool.Ping(context.Background())
	require.NoError(t, err)
}

func TestTokenOperations(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	defer cleanupDB(t, db)

	ctx := context.Background()

	// Test token insertion
	tokenAddr := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	err := db.InsertToken(ctx, tokenAddr, "Test Token", "TEST", 18, "standard")
	require.NoError(t, err)

	// Test token update
	err = db.InsertToken(ctx, tokenAddr, "Test Token 2", "TEST2", 18, "stable")
	require.NoError(t, err)
}

func TestPairOperations(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	defer cleanupDB(t, db)

	ctx := context.Background()

	// Create test tokens first
	token0 := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	token1 := []byte{20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}

	err := db.InsertToken(ctx, token0, "Token Zero", "TKN0", 18, "standard")
	require.NoError(t, err)
	err = db.InsertToken(ctx, token1, "Token One", "TKN1", 18, "standard")
	require.NoError(t, err)

	// Test pair insertion
	pairAddr := []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	err = db.InsertPair(ctx, pairAddr, token0, token1, 12345)
	require.NoError(t, err)
}

func TestPriceOperations(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	defer cleanupDB(t, db)

	ctx := context.Background()

	// Create test tokens and pair first
	token0 := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	token1 := []byte{20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
	pairAddr := []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}

	err := db.InsertToken(ctx, token0, "Token Zero", "TKN0", 18, "standard")
	require.NoError(t, err)
	err = db.InsertToken(ctx, token1, "Token One", "TKN1", 18, "standard")
	require.NoError(t, err)
	err = db.InsertPair(ctx, pairAddr, token0, token1, 12345)
	require.NoError(t, err)

	// Test price insertion
	now := time.Now()
	err = db.InsertPairPrice(ctx, pairAddr, 12345, now,
		1000000, 2000000, // reserves
		1.5, 0.75, // prices
		3000000, // TVL
		0.95,    // confidence
	)
	require.NoError(t, err)

	// Test getting latest price
	blockNum, blockTime, reserves0, reserves1, price0, price1, tvl, conf, err := db.GetLatestPairPrice(ctx, pairAddr)
	require.NoError(t, err)
	require.Equal(t, int64(12345), blockNum)
	require.WithinDuration(t, now, blockTime, time.Hour)
	require.Equal(t, float64(1000000), reserves0)
	require.Equal(t, float64(2000000), reserves1)
	require.Equal(t, 1.5, price0)
	require.Equal(t, 0.75, price1)
	require.Equal(t, float64(3000000), tvl)
	require.Equal(t, 0.95, conf)
}

func TestPairProgressOperations(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	defer cleanupDB(t, db)

	ctx := context.Background()

	// Create test pair first
	token0 := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	token1 := []byte{20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
	pairAddr := []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}

	err := db.InsertToken(ctx, token0, "TKN0", 18, "standard")
	require.NoError(t, err)
	err = db.InsertToken(ctx, token1, "TKN1", 18, "standard")
	require.NoError(t, err)
	err = db.InsertPair(ctx, pairAddr, token0, token1, 12345)
	require.NoError(t, err)

	// Test updating progress
	err = db.UpdatePairProgress(ctx, pairAddr, 12345)
	require.NoError(t, err)

	// Test getting progress
	lastBlock, err := db.GetPairProgress(ctx, pairAddr)
	require.NoError(t, err)
	require.Equal(t, uint64(12345), lastBlock)

	// Test updating progress again
	err = db.UpdatePairProgress(ctx, pairAddr, 12346)
	require.NoError(t, err)

	// Verify update
	lastBlock, err = db.GetPairProgress(ctx, pairAddr)
	require.NoError(t, err)
	require.Equal(t, uint64(12346), lastBlock)
}

func TestErrorCases(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	defer cleanupDB(t, db)

	ctx := context.Background()

	// Test getting non-existent pair price
	nonExistentPair := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	_, _, _, _, _, _, _, _, err := db.GetLatestPairPrice(ctx, nonExistentPair)
	require.Error(t, err) // Should return error for non-existent pair

	// Test getting non-existent pair progress
	_, err = db.GetPairProgress(ctx, nonExistentPair)
	require.Error(t, err) // Should return error for non-existent pair

	// Test invalid token insertion (nil address)
	err = db.InsertToken(ctx, nil, "Test Token", "TEST", 18, "standard")
	require.Error(t, err)

	// Test invalid pair insertion (missing tokens)
	pairAddr := []byte{1, 1, 1, 1}
	token0 := []byte{1, 2, 3, 4}
	token1 := []byte{5, 6, 7, 8}
	err = db.InsertPair(ctx, pairAddr, token0, token1, 12345)
	require.Error(t, err) // Should fail due to foreign key constraint
}

func TestConcurrentOperations(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	defer cleanupDB(t, db)

	ctx := context.Background()

	// Create test tokens and pair
	token0 := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	token1 := []byte{20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
	pairAddr := []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}

	err := db.InsertToken(ctx, token0, "Token Zero", "TKN0", 18, "standard")
	require.NoError(t, err)
	err = db.InsertToken(ctx, token1, "Token One", "TKN1", 18, "standard")
	require.NoError(t, err)
	err = db.InsertPair(ctx, pairAddr, token0, token1, 12345)
	require.NoError(t, err)

	// Test concurrent price updates
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			now := time.Now()
			err := db.InsertPairPrice(ctx, pairAddr, int64(12345+i), now,
				float64(1000000+i), float64(2000000+i),
				1.5, 0.75,
				float64(3000000+i),
				0.95,
			)
			require.NoError(t, err)
		}(i)
	}
	wg.Wait()

	// Test concurrent progress updates
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := db.UpdatePairProgress(ctx, pairAddr, uint64(12345+i))
			require.NoError(t, err)
		}(i)
	}
	wg.Wait()

	// Verify final state
	lastBlock, err := db.GetPairProgress(ctx, pairAddr)
	require.NoError(t, err)
	require.True(t, lastBlock >= 12345 && lastBlock <= 12354) // Should be one of the updates
}

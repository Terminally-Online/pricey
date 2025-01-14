package historical

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"pricey/internal/database"

	"github.com/ethereum/go-ethereum/common"
)

// Config holds configuration for the historical data collection service
type Config struct {
	BatchSize          int           // Number of blocks to process in a batch
	MaxConcurrentPairs int           // Maximum number of pairs to process concurrently
	RateLimit          time.Duration // Minimum time between RPC calls
	RetryDelay         time.Duration // Time to wait before retrying failed requests
	MaxRetries         int           // Maximum number of retries for failed requests
	MaxMulticallSize   int           // Maximum number of calls to batch in a single multicall
}

// DefaultConfig returns a default configuration
func DefaultConfig() Config {
	return Config{
		BatchSize:          100,
		MaxConcurrentPairs: 10,
		RateLimit:          time.Millisecond * 100,
		RetryDelay:         time.Second * 5,
		MaxRetries:         3,
		MaxMulticallSize:   500, // Maximum calls in a single multicall request
	}
}

// EthClient defines the interface for Ethereum client operations
type EthClient interface {
	// Multicall operations
	Multicall(ctx context.Context, blockNumber uint64, calls []Call) ([]Result, error)
	EncodeGetReservesCall(pairAddress common.Address) ([]byte, error)
	DecodeGetReservesResult(data []byte) (reserve0, reserve1 *big.Int, blockTimestampLast uint32, err error)
	GetBlockTimes(ctx context.Context, blockNumbers []uint64) (map[uint64]time.Time, error)
}

// Service manages historical data collection
type Service struct {
	db        *database.DB
	ethClient EthClient
	config    Config
}

// New creates a new historical data collection service
func New(db *database.DB, ethClient EthClient, config Config) *Service {
	return &Service{
		db:        db,
		ethClient: ethClient,
		config:    config,
	}
}

// CollectHistoricalData collects historical data for a pair over a range of blocks
func (s *Service) CollectHistoricalData(ctx context.Context, pairAddr common.Address, startBlock, endBlock uint64, backwards bool) error {
	// Calculate number of batches
	totalBlocks := endBlock - startBlock + 1
	numBatches := (totalBlocks + uint64(s.config.BatchSize) - 1) / uint64(s.config.BatchSize)

	// Process batches
	for i := uint64(0); i < numBatches; i++ {
		var batchStart, batchEnd uint64
		if backwards {
			batchEnd = endBlock - (i * uint64(s.config.BatchSize))
			batchStart = batchEnd - uint64(s.config.BatchSize) + 1
			if batchStart < startBlock {
				batchStart = startBlock
			}
		} else {
			batchStart = startBlock + (i * uint64(s.config.BatchSize))
			batchEnd = batchStart + uint64(s.config.BatchSize) - 1
			if batchEnd > endBlock {
				batchEnd = endBlock
			}
		}

		if err := s.processBatch(ctx, pairAddr, batchStart, batchEnd); err != nil {
			return fmt.Errorf("error processing batch %d: %w", i, err)
		}

		// Store progress checkpoint
		if err := s.db.UpdatePairProgress(ctx, pairAddr.Bytes(), batchEnd); err != nil {
			return fmt.Errorf("error updating progress: %w", err)
		}
	}

	return nil
}

// processBatch processes a batch of blocks for a pair
func (s *Service) processBatch(ctx context.Context, pairAddr common.Address, startBlock, endBlock uint64) error {
	// Get block times in batch
	blockNumbers := make([]uint64, 0, endBlock-startBlock+1)
	for block := startBlock; block <= endBlock; block++ {
		blockNumbers = append(blockNumbers, block)
	}

	blockTimes, err := s.ethClient.GetBlockTimes(ctx, blockNumbers)
	if err != nil {
		return fmt.Errorf("error getting block times: %w", err)
	}

	// Prepare multicall data for getting reserves
	calls := make([]Call, 0, endBlock-startBlock+1)
	for block := startBlock; block <= endBlock; block++ {
		callData, err := s.ethClient.EncodeGetReservesCall(pairAddr)
		if err != nil {
			return fmt.Errorf("error encoding getReserves call for block %d: %w", block, err)
		}
		calls = append(calls, Call{
			Target:   pairAddr,
			CallData: callData,
		})
	}

	// Split calls into chunks if needed
	for i := 0; i < len(calls); i += s.config.MaxMulticallSize {
		end := i + s.config.MaxMulticallSize
		if end > len(calls) {
			end = len(calls)
		}

		chunk := calls[i:end]
		results, err := s.ethClient.Multicall(ctx, startBlock+uint64(i), chunk)
		if err != nil {
			return fmt.Errorf("error executing multicall for blocks %d-%d: %w",
				startBlock+uint64(i), startBlock+uint64(end)-1, err)
		}

		// Process results
		for j, result := range results {
			if !result.Success {
				return fmt.Errorf("multicall failed for block %d", startBlock+uint64(i)+uint64(j))
			}

			reserves0, reserves1, _, err := s.ethClient.DecodeGetReservesResult(result.ReturnData)
			if err != nil {
				return fmt.Errorf("error decoding reserves for block %d: %w",
					startBlock+uint64(i)+uint64(j), err)
			}

			blockNum := startBlock + uint64(i) + uint64(j)
			blockTime := blockTimes[blockNum]

			// Store in database
			if err := s.db.InsertPairPrice(ctx, pairAddr.Bytes(), int64(blockNum), blockTime,
				float64(reserves0.Uint64()), float64(reserves1.Uint64()),
				0, 0, // Prices will be calculated by a separate service
				0,   // TVL will be calculated by a separate service
				1.0, // Historical data has high confidence
			); err != nil {
				return fmt.Errorf("error inserting pair price for block %d: %w", blockNum, err)
			}
		}
	}

	return nil
}

package historical

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	erc20bindings "pricey/contracts/bindings/erc"
	"pricey/internal/service/historical/types"

	"github.com/ethereum/go-ethereum/common"
)

// BackfillManager manages the process of backfilling historical data
type BackfillManager struct {
	db        types.BackfillDB
	ethClient types.EthClient
	config    Config
}

// NewBackfillManager creates a new backfill manager
func NewBackfillManager(db types.BackfillDB, ethClient types.EthClient, config Config) *BackfillManager {
	return &BackfillManager{
		db:        db,
		ethClient: ethClient,
		config:    config,
	}
}

// Run starts the backfill process
func (m *BackfillManager) Run(ctx context.Context) error {
	log.Printf("Starting backfill process...")

	// Get current block
	currentBlock, err := m.ethClient.GetCurrentBlock(ctx)
	if err != nil {
		return fmt.Errorf("error getting current block: %w", err)
	}

	// Start scanning from current block backwards
	scanBatchSize := uint64(5000) // Reduced from 10000
	lastScannedBlock := currentBlock

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Calculate block range for this batch
			startBlock := lastScannedBlock - scanBatchSize
			if startBlock > lastScannedBlock { // Handle underflow
				startBlock = 0
			}

			// Scan for new pairs
			if err := m.ScanForPairs(ctx, startBlock, lastScannedBlock); err != nil {
				log.Printf("Error scanning for pairs: %v", err)
				// Add delay after error
				time.Sleep(time.Second * 5)
			}

			// Add delay after scanning to respect rate limits
			time.Sleep(time.Second * 5)

			// Get pairs that need backfilling
			pairs, err := m.db.GetPairsForBackfill(ctx, m.config.MaxConcurrentPairs)
			if err != nil {
				log.Printf("Error getting pairs for backfill: %v", err)
				time.Sleep(time.Second * 5)
				continue
			}

			if len(pairs) > 0 {
				log.Printf("Processing %d pairs...", len(pairs))

				// Process pairs with limited concurrency
				sem := make(chan struct{}, m.config.MaxConcurrentPairs) // Reduced concurrency
				var wg sync.WaitGroup
				errCh := make(chan error, len(pairs))

				for _, pair := range pairs {
					wg.Add(1)
					go func(pairAddr common.Address) {
						defer wg.Done()

						// Acquire semaphore
						sem <- struct{}{}
						defer func() { <-sem }()

						// Process with exponential backoff
						var lastErr error
						for attempt := 0; attempt < m.config.MaxRetries; attempt++ {
							if attempt > 0 {
								backoffDuration := m.config.RetryDelay * time.Duration(1<<uint(attempt-1))
								log.Printf("Retrying pair %s (attempt %d/%d) after %v...",
									pairAddr.Hex(), attempt+1, m.config.MaxRetries, backoffDuration)
								// Wait before retrying with exponential backoff
								select {
								case <-ctx.Done():
									errCh <- ctx.Err()
									return
								case <-time.After(backoffDuration):
								}
							}

							if err := m.processPair(ctx, pairAddr); err != nil {
								lastErr = fmt.Errorf("error processing pair %s (attempt %d/%d): %w",
									pairAddr.Hex(), attempt+1, m.config.MaxRetries, err)
								log.Printf("Error: %v", lastErr)
								continue
							}
							log.Printf("Successfully processed pair %s", pairAddr.Hex())
							return // Success
						}
						if lastErr != nil {
							errCh <- lastErr
						}
					}(pair)
				}

				// Wait for all pairs to be processed
				wg.Wait()
				close(errCh)

				// Check for errors
				var errors []error
				for err := range errCh {
					if err != nil {
						errors = append(errors, err)
					}
				}

				if len(errors) > 0 {
					// Log errors but continue processing
					log.Printf("Errors occurred during backfill:")
					for _, err := range errors {
						log.Printf("- %v", err)
					}
					// Add delay after errors
					time.Sleep(time.Second * 10)
				}
			}

			// Update last scanned block and add delay
			lastScannedBlock = startBlock
			if lastScannedBlock == 0 {
				log.Printf("Reached genesis block, waiting for new blocks...")
				time.Sleep(time.Second * 30)
				lastScannedBlock = currentBlock
			} else {
				time.Sleep(time.Second * 10) // Increased delay between batches
			}
		}
	}
}

// processPair processes historical data for a single pair
func (m *BackfillManager) processPair(ctx context.Context, pairAddr common.Address) error {
	// Get the last processed block for this pair
	lastProcessed, err := m.db.GetPairProgress(ctx, pairAddr.Bytes())
	if err != nil {
		return fmt.Errorf("error getting pair progress: %w", err)
	}

	// Get current block number
	currentBlock, err := m.ethClient.GetCurrentBlock(ctx)
	if err != nil {
		return fmt.Errorf("error getting current block: %w", err)
	}

	log.Printf("Processing pair %s from block %d to %d", pairAddr.Hex(), lastProcessed, currentBlock)

	// Create a new service instance for this pair
	service := New(m.db, m.ethClient, m.config)

	// Process historical data backwards from the current block
	// We process in chunks to avoid overwhelming the node
	chunkSize := uint64(50000) // Process 50k blocks at a time
	for end := currentBlock; end > lastProcessed; {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			start := end - chunkSize
			if start < lastProcessed {
				start = lastProcessed
			}

			log.Printf("Processing blocks %d to %d for pair %s", start, end, pairAddr.Hex())
			if err := service.CollectHistoricalData(ctx, pairAddr, start, end, true); err != nil {
				return fmt.Errorf("error collecting historical data for blocks %d-%d: %w", start, end, err)
			}

			end = start
		}
	}

	log.Printf("Completed processing pair %s", pairAddr.Hex())
	return nil
}

// getTokenMetadata fetches a token's symbol and decimals from the blockchain
func (m *BackfillManager) getTokenMetadata(ctx context.Context, tokenAddr common.Address) (string, int, error) {
	// Create token contract instance
	token, err := erc20bindings.NewBindings(tokenAddr, m.ethClient)
	if err != nil {
		return "", 0, fmt.Errorf("failed to create token contract: %w", err)
	}

	// Get token symbol
	symbol, err := token.Symbol(nil)
	if err != nil {
		return "", 0, fmt.Errorf("failed to get token symbol: %w", err)
	}

	// Get token decimals
	decimals, err := token.Decimals(nil)
	if err != nil {
		return "", 0, fmt.Errorf("failed to get token decimals: %w", err)
	}

	return symbol, int(decimals), nil
}

// insertTokenWithMetadata fetches token metadata and inserts into database
func (m *BackfillManager) insertTokenWithMetadata(ctx context.Context, tokenAddr common.Address) error {
	symbol, decimals, err := m.getTokenMetadata(ctx, tokenAddr)
	if err != nil {
		return fmt.Errorf("failed to get token metadata: %w", err)
	}

	err = m.db.InsertToken(ctx, tokenAddr.Bytes(), symbol, decimals, "standard")
	if err != nil {
		return fmt.Errorf("failed to insert token: %w", err)
	}

	return nil
}

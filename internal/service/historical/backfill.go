package historical

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"

	"pricey/internal/service/historical/types"
)

type BackfillManager struct {
	db        types.BackfillDB
	ethClient types.EthClient
	config    types.Config
}

func NewBackfillManager(db types.BackfillDB, ethClient types.EthClient, config types.Config) *BackfillManager {
	return &BackfillManager{
		db:        db,
		ethClient: ethClient,
		config:    config,
	}
}

// Run starts the backfill process
func (m *BackfillManager) Run(ctx context.Context) error {
	log.Printf("🚀 Starting backfill process...")

	// Get current block
	currentBlock, err := m.ethClient.GetCurrentBlock(ctx)
	if err != nil {
		return fmt.Errorf("error getting current block: %w", err)
	}

	// Get last processed block from global sync
	lastScannedBlock, err := m.db.GetGlobalSyncProgress(ctx)
	if err != nil {
		return fmt.Errorf("error getting global sync progress: %w", err)
	}

	// If no progress, start from current block
	if lastScannedBlock == 0 {
		lastScannedBlock = currentBlock
	}

	log.Printf("📦 Resuming from block %d", lastScannedBlock)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Get current block periodically
			currentBlock, err = m.ethClient.GetCurrentBlock(ctx)
			if err != nil {
				log.Printf("❌ Error getting current block: %v", err)
				time.Sleep(time.Second * 10)
				continue
			}

			// Calculate block range to scan
			startBlock := lastScannedBlock - uint64(m.config.BlockRange)

			log.Printf("🔍 Scanning blocks %d to %d for new pairs...", startBlock, lastScannedBlock)

			// Scan for new pairs
			pairs, err := m.ScanForPairs(ctx, startBlock, lastScannedBlock)
			if err != nil {
				log.Printf("❌ Error scanning for pairs: %v", err)
				time.Sleep(time.Second * 10)
				continue
			}

			// Process pairs
			if len(pairs) > 0 {
				log.Printf("⚡ Processing %d pairs...", len(pairs))

				// Process pairs in parallel
				var wg sync.WaitGroup
				errCh := make(chan error, len(pairs))

				for _, pair := range pairs {
					wg.Add(1)
					go func(pairAddr common.Address) {
						defer wg.Done()

						var lastErr error
						for attempt := 0; attempt < m.config.MaxRetries; attempt++ {
							if attempt > 0 {
								log.Printf("🔄 Retrying pair %s (attempt %d/%d) after %v...",
									pairAddr.Hex(), attempt+1, m.config.MaxRetries, m.config.RetryDelay)
								time.Sleep(m.config.RetryDelay)
							}

							if err := m.processPair(ctx, pairAddr); err != nil {
								lastErr = fmt.Errorf("error processing pair %s (attempt %d/%d): %w",
									pairAddr.Hex(), attempt+1, m.config.MaxRetries, err)
								log.Printf("❌ %v", lastErr)
								continue
							}
							log.Printf("✅ Successfully processed pair %s", pairAddr.Hex())
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
					log.Printf("⚠️ Errors occurred during backfill:")
					for _, err := range errors {
						log.Printf("  - %v", err)
					}
					time.Sleep(time.Second * 10)
				}
			} else {
				log.Printf("💤 No pairs to process, waiting...")
			}

			// Update global sync progress
			if err := m.db.UpdateGlobalSyncProgress(ctx, lastScannedBlock); err != nil {
				log.Printf("❌ Error updating global sync progress: %v", err)
			}

			// Update last scanned block and add delay
			lastScannedBlock = startBlock
			if lastScannedBlock == 0 {
				log.Printf("🏁 Reached genesis block, waiting for new blocks...")
				time.Sleep(time.Second * 30)
				lastScannedBlock = currentBlock
			} else {
				log.Printf("📊 Progress: Scanned down to block %d", lastScannedBlock)
				time.Sleep(time.Second * 10)
			}
		}
	}
}

// ScanForPairs scans the given block range for new pairs
func (m *BackfillManager) ScanForPairs(ctx context.Context, startBlock, endBlock uint64) ([]common.Address, error) {
	// Create filter query for PairCreated events
	query := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(startBlock),
		ToBlock:   new(big.Int).SetUint64(endBlock),
		Addresses: []common.Address{UniswapV2Factory},
		Topics:    [][]common.Hash{{PairCreatedTopic}},
	}

	// Get logs
	logs, err := m.ethClient.FilterLogs(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("error getting logs: %w", err)
	}

	// Collect unique tokens and pairs
	uniqueTokens := make(map[common.Address]bool)
	var pairs []common.Address
	var pairData []struct {
		pair   common.Address
		token0 common.Address
		token1 common.Address
		block  uint64
	}

	for _, vLog := range logs {
		pairAddr := common.BytesToAddress(vLog.Data[:32])
		token0 := common.BytesToAddress(vLog.Topics[1].Bytes())
		token1 := common.BytesToAddress(vLog.Topics[2].Bytes())

		uniqueTokens[token0] = true
		uniqueTokens[token1] = true
		pairs = append(pairs, pairAddr)
		pairData = append(pairData, struct {
			pair   common.Address
			token0 common.Address
			token1 common.Address
			block  uint64
		}{
			pair:   pairAddr,
			token0: token0,
			token1: token1,
			block:  vLog.BlockNumber,
		})
	}

	// Convert unique tokens to slice
	var tokens []common.Address
	for token := range uniqueTokens {
		tokens = append(tokens, token)
	}

	// Process tokens in batches to avoid too large multicalls
	batchSize := 50
	for i := 0; i < len(tokens); i += batchSize {
		end := i + batchSize
		if end > len(tokens) {
			end = len(tokens)
		}
		batch := tokens[i:end]

		err := m.insertTokenWithMetadata(ctx, batch)
		if err != nil {
			log.Printf("Warning: failed to insert token batch: %v", err)
			continue
		}
	}

	// Insert pairs
	for _, pd := range pairData {
		if err := m.db.InsertPair(ctx, pd.pair.Bytes(), pd.token0.Bytes(), pd.token1.Bytes(), int64(pd.block)); err != nil {
			log.Printf("Warning: failed to insert pair %s: %v", pd.pair.Hex(), err)
			continue
		}

		// Initialize pair progress with creation block
		if err := m.db.UpdatePairProgress(ctx, pd.pair.Bytes(), pd.block); err != nil {
			log.Printf("Warning: failed to initialize progress for pair %s: %v", pd.pair.Hex(), err)
			continue
		}
	}

	return pairs, nil
}

// processPair processes historical data for a single pair
func (m *BackfillManager) processPair(ctx context.Context, pairAddr common.Address) error {
	// Get the last processed block for this pair
	lastProcessed, err := m.db.GetPairProgress(ctx, pairAddr.Bytes())
	if err != nil {
		if !strings.Contains(err.Error(), "no rows in result set") {
			return fmt.Errorf("error getting pair progress: %w", err)
		}
		// If no progress exists, get the pair's creation block
		pair, err := m.db.GetPair(ctx, pairAddr.Bytes())
		if err != nil {
			return fmt.Errorf("error getting pair: %w", err)
		}
		lastProcessed = uint64(pair.CreatedAtBlock)

		// Initialize progress with creation block
		if err := m.db.UpdatePairProgress(ctx, pairAddr.Bytes(), lastProcessed); err != nil {
			return fmt.Errorf("error initializing pair progress: %w", err)
		}
	}

	// Get current block number
	currentBlock, err := m.ethClient.GetCurrentBlock(ctx)
	if err != nil {
		return fmt.Errorf("error getting current block: %w", err)
	}

	log.Printf("Processing pair %s from block %d to %d", pairAddr.Hex(), lastProcessed, currentBlock)

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
			if err := m.db.UpdatePairProgress(ctx, pairAddr.Bytes(), end); err != nil {
				return fmt.Errorf("error updating pair progress: %w", err)
			}

			end = start
		}
	}

	log.Printf("Completed processing pair %s", pairAddr.Hex())
	return nil
}

// getTokenMetadata fetches a token's metadata from the blockchain
func (m *BackfillManager) getTokenMetadata(ctx context.Context, tokenAddr common.Address) (string, string, int, error) {
	metadata, err := m.ethClient.GetTokenMetadata(ctx, tokenAddr)
	if err != nil {
		return "", "", 0, fmt.Errorf("failed to get token metadata: %w", err)
	}
	return metadata.Name, metadata.Symbol, int(metadata.Decimals), nil
}

// insertTokenWithMetadata fetches token metadata and inserts into database
func (m *BackfillManager) insertTokenWithMetadata(ctx context.Context, tokenAddrs []common.Address) error {
	// First check which tokens we already have in the database
	var tokensToFetch []common.Address
	for _, addr := range tokenAddrs {
		token, err := m.db.GetToken(ctx, addr.Bytes())
		if err != nil || token.Symbol == "" {
			tokensToFetch = append(tokensToFetch, addr)
		}
	}

	if len(tokensToFetch) == 0 {
		return nil // All tokens already in database
	}

	// Fetch metadata for all tokens in one multicall
	metadata, err := m.ethClient.GetTokensMetadata(ctx, tokensToFetch)
	if err != nil {
		return fmt.Errorf("failed to get tokens metadata: %w", err)
	}

	// Insert all tokens
	for i, addr := range tokensToFetch {
		// Use fallback values for empty or invalid metadata
		name := metadata[i].Name
		symbol := metadata[i].Symbol
		if symbol == "" {
			symbol = fmt.Sprintf("UNK-%s", addr.Hex()[:8])
		}
		if name == "" {
			name = symbol
		}

		err = m.db.InsertToken(ctx, addr.Bytes(), name, symbol, int(metadata[i].Decimals), "standard")
		if err != nil {
			log.Printf("Warning: failed to insert token %s: %v", addr.Hex(), err)
			continue
		}
	}

	return nil
}

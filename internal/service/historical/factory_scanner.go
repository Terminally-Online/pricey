package historical

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"time"

	histtypes "pricey/internal/service/historical/types"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

var (
	// UniswapV2Factory is the address of the Uniswap V2 factory contract
	UniswapV2Factory = common.HexToAddress("0x5C69bEe701ef814a2B6a3EDD4B1652CB9cc5aA6f")

	// PairCreated event signature: PairCreated(address,address,address,uint256)
	PairCreatedTopic = common.HexToHash("0x0d3648bd0f6ba80134a33ba9275ac585d9d315f0ad8355cddefde31afa28d0e9")
)

// getTokenMetadataBatch fetches metadata for multiple tokens using multicall
func (m *BackfillManager) getTokenMetadataBatch(ctx context.Context, tokens []common.Address) (map[common.Address]struct {
	symbol   string
	decimals int
}, error) {
	results := make(map[common.Address]struct {
		symbol   string
		decimals int
	})

	// Process tokens in smaller batches to avoid hitting gas limits
	batchSize := 20
	for i := 0; i < len(tokens); i += batchSize {
		end := i + batchSize
		if end > len(tokens) {
			end = len(tokens)
		}

		batch := tokens[i:end]
		log.Printf("Processing metadata batch %d-%d (%d tokens)", i, end, len(batch))

		// First check if contracts exist
		codeCalls := make([]histtypes.Call, len(batch))
		for j, token := range batch {
			codeCalls[j] = histtypes.Call{
				Target:   token,
				CallData: []byte{}, // Empty call data for code existence check
			}
		}

		// Use tryAggregate to allow failures
		codeResults, err := m.ethClient.TryAggregate(ctx, false, codeCalls)
		if err != nil {
			log.Printf("Warning: failed to check contract existence: %v", err)
			continue
		}

		// Filter out tokens without code
		validTokens := make([]common.Address, 0, len(batch))
		for j, token := range batch {
			if j >= len(codeResults) || !codeResults[j].Success || len(codeResults[j].ReturnData) == 0 {
				log.Printf("Warning: token %s has no code", token.Hex())
				continue
			}
			validTokens = append(validTokens, token)
		}

		if len(validTokens) == 0 {
			continue
		}

		// Prepare multicall data for symbols
		symbolCalls := make([]histtypes.Call, len(validTokens))
		for j, token := range validTokens {
			callData, err := m.ethClient.EncodeERC20Call("symbol", nil)
			if err != nil {
				log.Printf("Warning: failed to encode symbol call for token %s: %v", token.Hex(), err)
				continue
			}
			symbolCalls[j] = histtypes.Call{
				Target:   token,
				CallData: callData,
			}
		}

		// Use tryAggregate for symbols
		symbolResults, err := m.ethClient.TryAggregate(ctx, false, symbolCalls)
		if err != nil {
			log.Printf("Warning: failed to execute symbol multicall: %v", err)
			continue
		}

		// Prepare multicall data for decimals
		decimalsCalls := make([]histtypes.Call, len(validTokens))
		for j, token := range validTokens {
			callData, err := m.ethClient.EncodeERC20Call("decimals", nil)
			if err != nil {
				log.Printf("Warning: failed to encode decimals call for token %s: %v", token.Hex(), err)
				continue
			}
			decimalsCalls[j] = histtypes.Call{
				Target:   token,
				CallData: callData,
			}
		}

		// Use tryAggregate for decimals
		decimalsResults, err := m.ethClient.TryAggregate(ctx, false, decimalsCalls)
		if err != nil {
			log.Printf("Warning: failed to execute decimals multicall: %v", err)
			continue
		}

		// Process results
		for j, token := range validTokens {
			if j >= len(symbolResults) || j >= len(decimalsResults) {
				continue
			}

			if !symbolResults[j].Success || !decimalsResults[j].Success {
				log.Printf("Warning: failed to get metadata for token %s", token.Hex())
				continue
			}

			symbol, err := m.ethClient.DecodeERC20Result("symbol", symbolResults[j].ReturnData)
			if err != nil {
				log.Printf("Warning: failed to decode symbol for token %s: %v", token.Hex(), err)
				continue
			}

			decimals, err := m.ethClient.DecodeERC20Result("decimals", decimalsResults[j].ReturnData)
			if err != nil {
				log.Printf("Warning: failed to decode decimals for token %s: %v", token.Hex(), err)
				continue
			}

			results[token] = struct {
				symbol   string
				decimals int
			}{
				symbol:   symbol.(string),
				decimals: int(decimals.(uint8)),
			}
		}

		// Add delay between batches
		if end < len(tokens) {
			time.Sleep(time.Second)
		}
	}

	return results, nil
}

// ScanForPairs scans for new Uniswap V2 pairs in the given block range
func (m *BackfillManager) ScanForPairs(ctx context.Context, startBlock, endBlock uint64) error {
	log.Printf("Scanning for pairs from block %d to %d", startBlock, endBlock)

	query := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(startBlock),
		ToBlock:   new(big.Int).SetUint64(endBlock),
		Addresses: []common.Address{UniswapV2Factory},
		Topics:    [][]common.Hash{{PairCreatedTopic}},
	}

	logs, err := m.ethClient.FilterLogs(ctx, query)
	if err != nil {
		return fmt.Errorf("error filtering logs: %w", err)
	}

	log.Printf("Found %d pair creation events", len(logs))

	// Collect unique tokens for batch processing
	uniqueTokens := make(map[common.Address]bool)
	for _, eventLog := range logs {
		token0 := common.BytesToAddress(eventLog.Topics[1].Bytes())
		token1 := common.BytesToAddress(eventLog.Topics[2].Bytes())
		uniqueTokens[token0] = true
		uniqueTokens[token1] = true
	}

	// Convert to slice for batch processing
	tokens := make([]common.Address, 0, len(uniqueTokens))
	for token := range uniqueTokens {
		tokens = append(tokens, token)
	}

	// Process tokens in batches of 100
	batchSize := 100
	var tokenMetadata = make(map[common.Address]struct {
		symbol   string
		decimals int
	})

	log.Printf("Processing %d unique tokens in batches of %d", len(tokens), batchSize)

	for i := 0; i < len(tokens); i += batchSize {
		end := i + batchSize
		if end > len(tokens) {
			end = len(tokens)
		}

		batch := tokens[i:end]
		log.Printf("Processing batch %d-%d (%d tokens)", i, end, len(batch))

		metadata, err := m.getTokenMetadataBatch(ctx, batch)
		if err != nil {
			log.Printf("Warning: error fetching metadata for batch: %v", err)
			continue
		}

		log.Printf("Successfully fetched metadata for %d tokens in batch", len(metadata))

		for addr, data := range metadata {
			tokenMetadata[addr] = data
			log.Printf("Token %s: symbol=%s decimals=%d", addr.Hex(), data.symbol, data.decimals)
		}

		// Add small delay between batches
		if end < len(tokens) {
			time.Sleep(time.Second)
		}
	}

	log.Printf("Successfully fetched metadata for %d tokens total", len(tokenMetadata))

	// Process pairs and insert into database
	for _, eventLog := range logs {
		token0 := common.BytesToAddress(eventLog.Topics[1].Bytes())
		token1 := common.BytesToAddress(eventLog.Topics[2].Bytes())
		pair := common.BytesToAddress(eventLog.Data[:32])

		log.Printf("Found pair %s (token0: %s, token1: %s) at block %d",
			pair.Hex(), token0.Hex(), token1.Hex(), eventLog.BlockNumber)

		// Track if tokens were successfully inserted
		token0Success := false
		token1Success := false

		// Insert tokens if we have metadata
		if metadata, ok := tokenMetadata[token0]; ok {
			if err := m.db.InsertToken(ctx, token0.Bytes(), metadata.symbol, metadata.decimals, "standard"); err != nil {
				log.Printf("Warning: failed to insert token0 %s: %v", token0.Hex(), err)
			} else {
				token0Success = true
			}
		}

		if metadata, ok := tokenMetadata[token1]; ok {
			if err := m.db.InsertToken(ctx, token1.Bytes(), metadata.symbol, metadata.decimals, "standard"); err != nil {
				log.Printf("Warning: failed to insert token1 %s: %v", token1.Hex(), err)
			} else {
				token1Success = true
			}
		}

		// Only insert pair if both tokens were successfully inserted
		if token0Success && token1Success {
			if err := m.db.InsertPair(ctx, pair.Bytes(), token0.Bytes(), token1.Bytes(), int64(eventLog.BlockNumber)); err != nil {
				log.Printf("Warning: failed to insert pair %s: %v", pair.Hex(), err)
			}
		} else {
			log.Printf("Skipping pair %s insertion due to token insertion failures", pair.Hex())
		}
	}

	return nil
}

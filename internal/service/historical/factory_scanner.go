package historical

import (
	"context"
	"log"

	histtypes "pricey/internal/service/historical/types"

	"github.com/ethereum/go-ethereum/common"
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

	// Process tokens in larger batches
	batchSize := 50
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
		// if end < len(tokens) {
		// 	time.Sleep(time.Second)
		// }
	}

	return results, nil
}

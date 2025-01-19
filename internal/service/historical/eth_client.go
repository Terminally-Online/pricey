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
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"

	erc20bindings "pricey/contracts/bindings/erc"
	multicallbindings "pricey/contracts/bindings/multicall"
	pairbindings "pricey/contracts/bindings/uniswap_pair"
	histtypes "pricey/internal/service/historical/types"
)

// Client implements the EthClient interface using go-ethereum
type Client struct {
	eth           *ethclient.Client
	multicall     *multicallbindings.Bindings
	multicallAddr common.Address
	multicallABI  *abi.ABI
	pair          *abi.ABI
	rateLimiter   *time.Ticker
	lastCall      time.Time
	mu            sync.Mutex
	erc20ABI      abi.ABI
}

// NewClient creates a new Client
func NewClient(ethClient *ethclient.Client, rateLimit time.Duration) (*Client, error) {
	multicallAddr := common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11")

	// Verify contract exists
	code, err := ethClient.CodeAt(context.Background(), multicallAddr, nil)
	if err != nil {
		return nil, fmt.Errorf("error checking Multicall contract: %w", err)
	}
	if len(code) == 0 {
		return nil, fmt.Errorf("no contract at Multicall address %s", multicallAddr.Hex())
	}

	// Create Multicall3 instance
	multicallContract, err := multicallbindings.NewBindings(multicallAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("error creating Multicall3 instance: %w", err)
	}

	// Load multicall ABI
	multicallABI, err := multicallbindings.BindingsMetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("error loading multicall ABI: %w", err)
	}

	// Load pair ABI
	pairABI, err := pairbindings.BindingsMetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("error loading pair ABI: %w", err)
	}

	// Parse ERC20 ABI
	erc20ABI, err := abi.JSON(strings.NewReader(erc20bindings.BindingsABI))
	if err != nil {
		return nil, fmt.Errorf("error parsing ERC20 ABI: %w", err)
	}

	return &Client{
		eth:           ethClient,
		multicall:     multicallContract,
		multicallAddr: multicallAddr,
		multicallABI:  multicallABI,
		pair:          pairABI,
		rateLimiter:   time.NewTicker(rateLimit),
		lastCall:      time.Now(),
		erc20ABI:      erc20ABI,
	}, nil
}

// Close cleans up resources
func (c *Client) Close() {
	if c.rateLimiter != nil {
		c.rateLimiter.Stop()
	}
}

// waitForRateLimit waits for the rate limiter with additional dynamic delay
func (c *Client) waitForRateLimit() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Wait for ticker
	<-c.rateLimiter.C

	// Add dynamic delay based on time since last call
	// elapsed := time.Since(c.lastCall)
	// if elapsed < time.Second {
	// 	// If less than a second has passed, add extra delay
	// 	time.Sleep(time.Second - elapsed)
	// }

	c.lastCall = time.Now()
}

// retryWithBackoff executes a function with exponential backoff
func (c *Client) retryWithBackoff(ctx context.Context, operation string, fn func() error) error {
	maxRetries := 3
	baseDelay := time.Second * 2

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := fn(); err != nil {
			lastErr = err
			// Check if we should retry based on error type
			if strings.Contains(err.Error(), "rate limit") ||
				strings.Contains(err.Error(), "429") ||
				strings.Contains(err.Error(), "too many requests") ||
				strings.Contains(err.Error(), "exceeded") {
				backoffDuration := baseDelay * time.Duration(1<<uint(attempt))
				log.Printf("Rate limit hit during %s, attempt %d/%d. Waiting %v before retry...",
					operation, attempt+1, maxRetries, backoffDuration)

				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(backoffDuration):
					continue
				}
			}
			return err // Non-retryable error
		}
		return nil // Success
	}
	return fmt.Errorf("operation %s failed after %d retries: %w", operation, maxRetries, lastErr)
}

// Multicall executes multiple calls in a single transaction
func (c *Client) Multicall(ctx context.Context, blockNumber uint64, calls []histtypes.Call) ([]histtypes.Result, error) {
	c.waitForRateLimit()

	var results []histtypes.Result
	err := c.retryWithBackoff(ctx, "multicall", func() error {
		// Convert calls to Multicall format
		multicallCalls := make([]struct {
			Target   common.Address `json:"target"`
			CallData []byte         `json:"callData"`
		}, len(calls))

		for i, call := range calls {
			multicallCalls[i] = struct {
				Target   common.Address `json:"target"`
				CallData []byte         `json:"callData"`
			}{
				Target:   call.Target,
				CallData: call.CallData,
			}
		}

		// Pack the call
		input, err := c.multicallABI.Pack("aggregate", multicallCalls)
		if err != nil {
			return fmt.Errorf("failed to pack aggregate call: %w", err)
		}

		// Make the call
		msg := ethereum.CallMsg{
			To:   &c.multicallAddr,
			Data: input,
		}

		log.Printf("Debug: Making multicall to %s with %d calls", c.multicallAddr.Hex(), len(multicallCalls))
		output, err := c.eth.CallContract(ctx, msg, nil)
		if err != nil {
			return fmt.Errorf("aggregate failed: %w", err)
		}
		log.Printf("Debug: Raw multicall response length: %d bytes", len(output))

		// Unpack the result
		unpacked, err := c.multicallABI.Unpack("aggregate", output)
		if err != nil {
			log.Printf("Debug: Raw response hex: %x", output)
			return fmt.Errorf("failed to unpack aggregate result: %w", err)
		}
		log.Printf("Debug: Unpacked %d values", len(unpacked))

		// Convert results
		blockNum, ok := unpacked[0].(*big.Int)
		if !ok {
			return fmt.Errorf("invalid block number type: %T", unpacked[0])
		}
		returnData, ok := unpacked[1].([][]byte)
		if !ok {
			return fmt.Errorf("invalid return data type: %T", unpacked[1])
		}

		log.Printf("Debug: Multicall executed at block %d", blockNum.Uint64())

		results = make([]histtypes.Result, len(returnData))
		for i, data := range returnData {
			results[i] = histtypes.Result{
				Success:    len(data) > 0, // Consider non-empty response as success
				ReturnData: data,
			}
		}
		return nil
	})

	return results, err
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// EncodeGetReservesCall encodes a getReserves call for a pair
func (c *Client) EncodeGetReservesCall(pairAddress common.Address) ([]byte, error) {
	return c.pair.Pack("getReserves")
}

// DecodeGetReservesResult decodes the result of a getReserves call
func (c *Client) DecodeGetReservesResult(data []byte) (reserve0, reserve1 *big.Int, blockTimestampLast uint32, err error) {
	results, err := c.pair.Unpack("getReserves", data)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("error unpacking getReserves result: %w", err)
	}

	if len(results) != 3 {
		return nil, nil, 0, fmt.Errorf("unexpected number of results: got %d, want 3", len(results))
	}

	var ok bool
	reserve0, ok = results[0].(*big.Int)
	if !ok {
		return nil, nil, 0, fmt.Errorf("reserve0 has wrong type: got %T, want *big.Int", results[0])
	}

	reserve1, ok = results[1].(*big.Int)
	if !ok {
		return nil, nil, 0, fmt.Errorf("reserve1 has wrong type: got %T, want *big.Int", results[1])
	}

	blockTimestampLast, ok = results[2].(uint32)
	if !ok {
		return nil, nil, 0, fmt.Errorf("blockTimestampLast has wrong type: got %T, want uint32", results[2])
	}

	return reserve0, reserve1, blockTimestampLast, nil
}

// GetBlockTimes gets timestamps for multiple blocks efficiently
func (c *Client) GetBlockTimes(ctx context.Context, blockNumbers []uint64) (map[uint64]time.Time, error) {
	results := make(map[uint64]time.Time)

	// Process blocks sequentially to avoid overwhelming the node
	for _, blockNum := range blockNumbers {
		c.waitForRateLimit()

		var header *types.Header
		err := c.retryWithBackoff(ctx, fmt.Sprintf("get block %d", blockNum), func() error {
			var err error
			header, err = c.eth.HeaderByNumber(ctx, big.NewInt(int64(blockNum)))
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("error getting block header for block %d: %w", blockNum, err)
		}

		results[blockNum] = time.Unix(int64(header.Time), 0)
	}

	return results, nil
}

// GetCurrentBlock gets the latest block number
func (c *Client) GetCurrentBlock(ctx context.Context) (uint64, error) {
	c.waitForRateLimit()

	header, err := c.eth.HeaderByNumber(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("error getting latest block: %w", err)
	}
	return header.Number.Uint64(), nil
}

// FilterLogs returns logs matching the filter criteria
func (c *Client) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	c.waitForRateLimit()
	return c.eth.FilterLogs(ctx, q)
}

// CallContract executes a contract call
func (c *Client) CallContract(ctx context.Context, call ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	c.waitForRateLimit()
	return c.eth.CallContract(ctx, call, blockNumber)
}

// CodeAt returns the code of the given account at the given block
func (c *Client) CodeAt(ctx context.Context, contract common.Address, blockNumber *big.Int) ([]byte, error) {
	c.waitForRateLimit()
	return c.eth.CodeAt(ctx, contract, blockNumber)
}

// PendingCodeAt returns the code of the given account in the pending state
func (c *Client) PendingCodeAt(ctx context.Context, account common.Address) ([]byte, error) {
	c.waitForRateLimit()
	return c.eth.PendingCodeAt(ctx, account)
}

// PendingNonceAt returns the account nonce of the given account in the pending state
func (c *Client) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	c.waitForRateLimit()
	return c.eth.PendingNonceAt(ctx, account)
}

// SuggestGasPrice returns the suggested gas price
func (c *Client) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	c.waitForRateLimit()
	return c.eth.SuggestGasPrice(ctx)
}

// EstimateGas returns an estimate of the amount of gas needed to execute the given call
func (c *Client) EstimateGas(ctx context.Context, call ethereum.CallMsg) (uint64, error) {
	c.waitForRateLimit()
	return c.eth.EstimateGas(ctx, call)
}

// SendTransaction sends a transaction to the network
func (c *Client) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	c.waitForRateLimit()
	return c.eth.SendTransaction(ctx, tx)
}

// HeaderByNumber returns the block header with the given number
func (c *Client) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	c.waitForRateLimit()
	return c.eth.HeaderByNumber(ctx, number)
}

// SubscribeFilterLogs subscribes to the results of a streaming filter query
func (c *Client) SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
	c.waitForRateLimit()
	return c.eth.SubscribeFilterLogs(ctx, q, ch)
}

// SuggestGasTipCap returns the suggested gas tip cap
func (c *Client) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	c.waitForRateLimit()
	return c.eth.SuggestGasTipCap(ctx)
}

// EncodeERC20Call encodes an ERC20 method call
func (c *Client) EncodeERC20Call(method string, args []interface{}) ([]byte, error) {
	// Pack the method call
	data, err := c.erc20ABI.Pack(method, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to pack %s call: %w", method, err)
	}
	return data, nil
}

// DecodeERC20Result decodes an ERC20 method result
func (c *Client) DecodeERC20Result(method string, data []byte) (interface{}, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty response data")
	}

	result, err := c.erc20ABI.Unpack(method, data)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack %s result: %w", method, err)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no results returned")
	}
	return result[0], nil
}

// TryAggregate implements the EthClient interface
func (c *Client) TryAggregate(ctx context.Context, requireSuccess bool, calls []histtypes.Call) ([]histtypes.Result, error) {
	return c.Multicall(ctx, 0, calls)
}

// GetPairTokens gets the token0 and token1 addresses for a pair
func (c *Client) GetPairTokens(ctx context.Context, pairAddr common.Address) (token0, token1 common.Address, err error) {
	// Encode both calls
	token0Call, err := c.EncodeERC20Call("token0", nil)
	if err != nil {
		return common.Address{}, common.Address{}, fmt.Errorf("error encoding token0 call: %w", err)
	}

	token1Call, err := c.EncodeERC20Call("token1", nil)
	if err != nil {
		return common.Address{}, common.Address{}, fmt.Errorf("error encoding token1 call: %w", err)
	}

	// Create multicall calls
	calls := []histtypes.Call{
		{Target: pairAddr, CallData: token0Call},
		{Target: pairAddr, CallData: token1Call},
	}

	// Execute multicall
	results, err := c.Multicall(ctx, 0, calls)
	if err != nil {
		return common.Address{}, common.Address{}, fmt.Errorf("multicall failed: %w", err)
	}

	// Check results
	if len(results) != 2 {
		return common.Address{}, common.Address{}, fmt.Errorf("unexpected number of results: got %d, want 2", len(results))
	}

	// Decode token0
	if !results[0].Success {
		return common.Address{}, common.Address{}, fmt.Errorf("token0 call failed")
	}
	token0Result, err := c.DecodeERC20Result("token0", results[0].ReturnData)
	if err != nil {
		return common.Address{}, common.Address{}, fmt.Errorf("error decoding token0: %w", err)
	}
	token0 = token0Result.(common.Address)

	// Decode token1
	if !results[1].Success {
		return common.Address{}, common.Address{}, fmt.Errorf("token1 call failed")
	}
	token1Result, err := c.DecodeERC20Result("token1", results[1].ReturnData)
	if err != nil {
		return common.Address{}, common.Address{}, fmt.Errorf("error decoding token1: %w", err)
	}
	token1 = token1Result.(common.Address)

	return token0, token1, nil
}

// GetTokenDecimals gets the number of decimals for a token
func (c *Client) GetTokenDecimals(ctx context.Context, tokenAddr common.Address) (uint8, error) {
	decimals, err := c.GetTokensDecimals(ctx, []common.Address{tokenAddr})
	if err != nil {
		return 0, err
	}
	return decimals[0], nil
}

// GetTokensDecimals gets the number of decimals for multiple tokens in a single multicall
func (c *Client) GetTokensDecimals(ctx context.Context, tokenAddrs []common.Address) ([]uint8, error) {
	// Encode decimals calls for all tokens
	calls := make([]histtypes.Call, len(tokenAddrs))
	for i, addr := range tokenAddrs {
		callData, err := c.EncodeERC20Call("decimals", nil)
		if err != nil {
			return nil, fmt.Errorf("error encoding decimals call for %s: %w", addr.Hex(), err)
		}
		calls[i] = histtypes.Call{
			Target:   addr,
			CallData: callData,
		}
	}

	// Execute multicall
	results, err := c.Multicall(ctx, 0, calls)
	if err != nil {
		return nil, fmt.Errorf("multicall failed: %w", err)
	}

	// Process results
	decimals := make([]uint8, len(results))
	for i, result := range results {
		if !result.Success {
			return nil, fmt.Errorf("decimals call failed for token %s", tokenAddrs[i].Hex())
		}
		decimalsResult, err := c.DecodeERC20Result("decimals", result.ReturnData)
		if err != nil {
			return nil, fmt.Errorf("error decoding decimals for token %s: %w", tokenAddrs[i].Hex(), err)
		}
		decimals[i] = decimalsResult.(uint8)
	}

	return decimals, nil
}

// GetTokenMetadata gets all metadata for a token using Multicall
func (c *Client) GetTokenMetadata(ctx context.Context, tokenAddr common.Address) (*histtypes.TokenMetadata, error) {
	metadata, err := c.GetTokensMetadata(ctx, []common.Address{tokenAddr})
	if err != nil {
		return nil, err
	}
	return metadata[0], nil
}

// GetTokensMetadata gets metadata for multiple tokens in a single multicall
func (c *Client) GetTokensMetadata(ctx context.Context, tokenAddrs []common.Address) ([]*histtypes.TokenMetadata, error) {
	// Process tokens in smaller batches to avoid overloading the multicall
	batchSize := 10 // Reduced from 20 to 10 tokens per batch to be more conservative
	var allMetadata []*histtypes.TokenMetadata

	for i := 0; i < len(tokenAddrs); i += batchSize {
		end := i + batchSize
		if end > len(tokenAddrs) {
			end = len(tokenAddrs)
		}
		batch := tokenAddrs[i:end]

		// Add delay between batches to avoid rate limiting
		if i > 0 {
			time.Sleep(time.Millisecond * 200)
		}

		metadata, err := c.getTokenMetadataBatch(ctx, batch)
		if err != nil {
			log.Printf("Warning: failed to get metadata for batch %d-%d: %v, retrying...", i, end, err)
			// Retry once with smaller batch
			if len(batch) > 1 {
				for _, addr := range batch {
					singleMetadata, err := c.getTokenMetadataBatch(ctx, []common.Address{addr})
					if err != nil {
						log.Printf("Warning: failed to get metadata for token %s: %v", addr.Hex(), err)
						// Use fallback metadata
						allMetadata = append(allMetadata, &histtypes.TokenMetadata{
							Name:     fmt.Sprintf("Unknown-%s", addr.Hex()[:8]),
							Symbol:   fmt.Sprintf("UNK-%s", addr.Hex()[:8]),
							Decimals: 18,
						})
						continue
					}
					allMetadata = append(allMetadata, singleMetadata...)
				}
				continue
			}
			return nil, fmt.Errorf("failed to get metadata for batch: %w", err)
		}
		allMetadata = append(allMetadata, metadata...)
	}

	return allMetadata, nil
}

// getTokenMetadataBatch gets metadata for a batch of tokens in a single multicall
func (c *Client) getTokenMetadataBatch(ctx context.Context, tokenAddrs []common.Address) ([]*histtypes.TokenMetadata, error) {
	// Create multicall for this batch
	calls := make([]histtypes.Call, 0, len(tokenAddrs)*3)
	for _, addr := range tokenAddrs {
		// Name call
		nameData, err := c.erc20ABI.Pack("name")
		if err != nil {
			return nil, fmt.Errorf("failed to pack name call: %w", err)
		}
		calls = append(calls, histtypes.Call{
			Target:   addr,
			CallData: nameData,
		})

		// Symbol call
		symbolData, err := c.erc20ABI.Pack("symbol")
		if err != nil {
			return nil, fmt.Errorf("failed to pack symbol call: %w", err)
		}
		calls = append(calls, histtypes.Call{
			Target:   addr,
			CallData: symbolData,
		})

		// Decimals call
		decimalsData, err := c.erc20ABI.Pack("decimals")
		if err != nil {
			return nil, fmt.Errorf("failed to pack decimals call: %w", err)
		}
		calls = append(calls, histtypes.Call{
			Target:   addr,
			CallData: decimalsData,
		})
	}

	// Execute multicall for this batch
	results, err := c.Multicall(ctx, 0, calls)
	if err != nil {
		return nil, fmt.Errorf("multicall failed for batch: %w", err)
	}

	var metadata []*histtypes.TokenMetadata
	// Process results for this batch
	for j := 0; j < len(tokenAddrs); j++ {
		addr := tokenAddrs[j]
		baseIdx := j * 3

		// Unpack name
		var name string
		if len(results[baseIdx].ReturnData) > 0 {
			nameUnpacked, err := c.erc20ABI.Unpack("name", results[baseIdx].ReturnData)
			if err != nil {
				log.Printf("Warning: failed to unpack name for token %s: %v", addr.Hex(), err)
				continue
			}
			if len(nameUnpacked) > 0 {
				name = nameUnpacked[0].(string)
			}
		}

		// Unpack symbol
		var symbol string
		if len(results[baseIdx+1].ReturnData) > 0 {
			symbolUnpacked, err := c.erc20ABI.Unpack("symbol", results[baseIdx+1].ReturnData)
			if err != nil {
				log.Printf("Warning: failed to unpack symbol for token %s: %v", addr.Hex(), err)
				continue
			}
			if len(symbolUnpacked) > 0 {
				symbol = symbolUnpacked[0].(string)
			}
		}

		// Unpack decimals
		var decimals uint8
		if len(results[baseIdx+2].ReturnData) > 0 {
			decimalsUnpacked, err := c.erc20ABI.Unpack("decimals", results[baseIdx+2].ReturnData)
			if err != nil {
				log.Printf("Warning: failed to unpack decimals for token %s: %v", addr.Hex(), err)
				continue
			}
			if len(decimalsUnpacked) > 0 {
				decimals = decimalsUnpacked[0].(uint8)
			}
		}

		metadata = append(metadata, &histtypes.TokenMetadata{
			Name:     name,
			Symbol:   symbol,
			Decimals: decimals,
		})
		log.Printf("Debug: Got metadata for token %s: %s (%s) with %d decimals", addr.Hex(), name, symbol, decimals)
	}

	return metadata, nil
}

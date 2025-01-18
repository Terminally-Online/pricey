package historical

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"strings"
	"sync"
	"time"

	erc20bindings "pricey/contracts/bindings/erc"
	multicallbindings "pricey/contracts/bindings/multicall"
	pairbindings "pricey/contracts/bindings/uniswap_pair"
	histtypes "pricey/internal/service/historical/types"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
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
}

// NewClient creates a new Client
func NewClient(ethClient *ethclient.Client, rateLimit time.Duration) (*Client, error) {
	multicallAddr := common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11")

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

	return &Client{
		eth:           ethClient,
		multicall:     multicallContract,
		multicallAddr: multicallAddr,
		multicallABI:  multicallABI,
		pair:          pairABI,
		rateLimiter:   time.NewTicker(rateLimit),
		lastCall:      time.Now(),
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
	elapsed := time.Since(c.lastCall)
	if elapsed < time.Second {
		// If less than a second has passed, add extra delay
		time.Sleep(time.Second - elapsed)
	}

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
		// Convert calls to Multicall3 format
		multicallCalls := make([]multicallbindings.Multicall3Call3, len(calls))
		for i, call := range calls {
			multicallCalls[i] = multicallbindings.Multicall3Call3{
				Target:       call.Target,
				AllowFailure: true,
				CallData:     call.CallData,
			}
		}

		// Pack the aggregate3 call
		data, err := c.multicallABI.Pack("aggregate3", false, multicallCalls)
		if err != nil {
			return fmt.Errorf("failed to pack aggregate3 call: %w", err)
		}

		// Make the call
		msg := ethereum.CallMsg{
			To:   &c.multicallAddr,
			Data: data,
		}
		result, err := c.eth.CallContract(ctx, msg, big.NewInt(int64(blockNumber)))
		if err != nil {
			return fmt.Errorf("multicall failed: %w", err)
		}

		// Unpack the result
		unpacked, err := c.multicallABI.Unpack("aggregate3", result)
		if err != nil {
			return fmt.Errorf("failed to unpack aggregate3 result: %w", err)
		}

		// Convert results
		rawResults, ok := unpacked[0].([]struct {
			Success    bool
			ReturnData []byte
		})
		if !ok {
			return fmt.Errorf("invalid result type")
		}

		results = make([]histtypes.Result, len(rawResults))
		for i, result := range rawResults {
			results[i] = histtypes.Result{
				Success:    result.Success,
				ReturnData: result.ReturnData,
			}
		}
		return nil
	})

	return results, err
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
	log.Printf("Encoding ERC20 call for method: %s", method)
	abi, err := abi.JSON(strings.NewReader(erc20bindings.BindingsABI))
	if err != nil {
		return nil, fmt.Errorf("error parsing ABI: %w", err)
	}
	data, err := abi.Pack(method, args...)
	if err != nil {
		return nil, fmt.Errorf("error packing method %s: %w", method, err)
	}
	log.Printf("Successfully encoded ERC20 call for method %s", method)
	return data, nil
}

// DecodeERC20Result decodes an ERC20 method result
func (c *Client) DecodeERC20Result(method string, data []byte) (interface{}, error) {
	log.Printf("Decoding ERC20 result for method: %s", method)
	abi, err := abi.JSON(strings.NewReader(erc20bindings.BindingsABI))
	if err != nil {
		return nil, fmt.Errorf("error parsing ABI: %w", err)
	}

	result, err := abi.Unpack(method, data)
	if err != nil {
		return nil, fmt.Errorf("error unpacking result for method %s: %w", method, err)
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no result returned for method %s", method)
	}

	log.Printf("Successfully decoded ERC20 result for method %s: %v", method, result[0])
	return result[0], nil
}

// TryAggregate executes multiple calls in a single transaction, allowing failures
func (c *Client) TryAggregate(ctx context.Context, requireSuccess bool, calls []histtypes.Call) ([]histtypes.Result, error) {
	c.waitForRateLimit()

	var results []histtypes.Result
	err := c.retryWithBackoff(ctx, "tryAggregate", func() error {
		// Convert calls to Multicall3 format
		multicallCalls := make([]multicallbindings.Multicall3Call, len(calls))
		for i, call := range calls {
			multicallCalls[i] = multicallbindings.Multicall3Call{
				Target:   call.Target,
				CallData: call.CallData,
			}
		}

		// Pack the tryAggregate call
		data, err := c.multicallABI.Pack("tryAggregate", requireSuccess, multicallCalls)
		if err != nil {
			return fmt.Errorf("failed to pack tryAggregate call: %w", err)
		}

		// Make the call
		msg := ethereum.CallMsg{
			To:   &c.multicallAddr,
			Data: data,
		}
		result, err := c.eth.CallContract(ctx, msg, nil)
		if err != nil {
			return fmt.Errorf("tryAggregate failed: %w", err)
		}

		// Unpack the result
		unpacked, err := c.multicallABI.Unpack("tryAggregate", result)
		if err != nil {
			return fmt.Errorf("failed to unpack tryAggregate result: %w", err)
		}

		// Convert results - handle the JSON-tagged struct type
		type resultStruct struct {
			Success    bool   `json:"success"`
			ReturnData []byte `json:"returnData"`
		}
		rawResults, ok := unpacked[0].([]resultStruct)
		if !ok {
			return fmt.Errorf("invalid result type: %T", unpacked[0])
		}

		results = make([]histtypes.Result, len(rawResults))
		for i, result := range rawResults {
			results[i] = histtypes.Result{
				Success:    result.Success,
				ReturnData: result.ReturnData,
			}
		}
		return nil
	})

	return results, err
}

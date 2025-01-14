package historical

import (
	"context"
	"fmt"
	"math/big"
	"time"

	multicallbindings "pricey/contracts/bindings/multicall"
	pairbindings "pricey/contracts/bindings/uniswap_pair"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Client implements the EthClient interface using go-ethereum
type Client struct {
	eth           *ethclient.Client
	multicall     *multicallbindings.Bindings
	multicallAddr common.Address
	multicallABI  *abi.ABI
	pair          *abi.ABI
}

// NewClient creates a new Client
func NewClient(ethClient *ethclient.Client) (*Client, error) {
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
	}, nil
}

// Call represents a call to be made in a multicall
type Call struct {
	Target   common.Address
	CallData []byte
}

// Result represents the result of a multicall
type Result struct {
	Success    bool
	ReturnData []byte
}

// Multicall executes multiple calls in a single transaction
func (c *Client) Multicall(ctx context.Context, blockNumber uint64, calls []Call) ([]Result, error) {
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
		return nil, fmt.Errorf("failed to pack aggregate3 call: %w", err)
	}

	// Make the call
	msg := ethereum.CallMsg{
		To:   &c.multicallAddr,
		Data: data,
	}
	result, err := c.eth.CallContract(ctx, msg, big.NewInt(int64(blockNumber)))
	if err != nil {
		return nil, fmt.Errorf("multicall failed: %w", err)
	}

	// Unpack the result
	unpacked, err := c.multicallABI.Unpack("aggregate3", result)
	if err != nil {
		return nil, fmt.Errorf("failed to unpack aggregate3 result: %w", err)
	}

	// Convert results
	rawResults, ok := unpacked[0].([]struct {
		Success    bool
		ReturnData []byte
	})
	if !ok {
		return nil, fmt.Errorf("invalid result type")
	}

	results := make([]Result, len(rawResults))
	for i, result := range rawResults {
		results[i] = Result{
			Success:    result.Success,
			ReturnData: result.ReturnData,
		}
	}

	return results, nil
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

	// Process blocks in parallel with a worker pool
	type blockResult struct {
		number uint64
		time   time.Time
		err    error
	}

	// Create a channel for results
	resultChan := make(chan blockResult, len(blockNumbers))

	// Use a semaphore to limit concurrent requests
	sem := make(chan struct{}, 10) // Max 10 concurrent requests

	// Start workers for each block
	for _, blockNum := range blockNumbers {
		go func(num uint64) {
			sem <- struct{}{}        // Acquire semaphore
			defer func() { <-sem }() // Release semaphore

			// Get block header
			header, err := c.eth.HeaderByNumber(ctx, big.NewInt(int64(num)))
			if err != nil {
				resultChan <- blockResult{number: num, err: fmt.Errorf("error getting block header: %w", err)}
				return
			}

			resultChan <- blockResult{
				number: num,
				time:   time.Unix(int64(header.Time), 0),
			}
		}(blockNum)
	}

	// Collect results
	for range blockNumbers {
		result := <-resultChan
		if result.err != nil {
			return nil, fmt.Errorf("error getting block time for block %d: %w", result.number, result.err)
		}
		results[result.number] = result.time
	}

	return results, nil
}

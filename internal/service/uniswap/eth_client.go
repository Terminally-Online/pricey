package uniswap

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"

	multicallbindings "pricey/contracts/bindings/multicall"
	pairbindings "pricey/contracts/bindings/uniswap_pair"
)

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

// MulticallClient extends EthClient with multicall functionality
type MulticallClient interface {
	EthClient
	Multicall(ctx context.Context, calls []Call) ([]Result, error)
	EncodeGetReservesCall(pairAddr common.Address) (Call, error)
	DecodeGetReservesResult(result Result) (*big.Int, *big.Int, error)
}

// Client implements MulticallClient
type Client struct {
	ethClient         EthClient
	multicallAddr     common.Address
	multicallContract *multicallbindings.Bindings
	multicallABI      *abi.ABI
	pairABI           *abi.ABI
}

// NewClient creates a new Client instance
func NewClient(ethClient EthClient, multicallAddr common.Address) (*Client, error) {
	multicallContract, err := multicallbindings.NewBindings(multicallAddr, ethClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create multicall contract: %w", err)
	}

	multicallABI, err := multicallbindings.BindingsMetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("failed to get multicall ABI: %w", err)
	}

	pairABI, err := pairbindings.BindingsMetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("failed to get pair ABI: %w", err)
	}

	return &Client{
		ethClient:         ethClient,
		multicallAddr:     multicallAddr,
		multicallContract: multicallContract,
		multicallABI:      multicallABI,
		pairABI:           pairABI,
	}, nil
}

// GetReserves gets the current reserves for a pair
func (c *Client) GetReserves(ctx context.Context, pairAddr common.Address) (*big.Int, *big.Int, error) {
	call, err := c.EncodeGetReservesCall(pairAddr)
	if err != nil {
		return nil, nil, err
	}

	results, err := c.Multicall(ctx, []Call{call})
	if err != nil {
		return nil, nil, err
	}

	if len(results) == 0 {
		return nil, nil, fmt.Errorf("no results returned")
	}

	return c.DecodeGetReservesResult(results[0])
}

// GetBlockTime gets the timestamp for a block number
func (c *Client) GetBlockTime(ctx context.Context, blockNumber uint64) (uint64, error) {
	header, err := c.ethClient.HeaderByNumber(ctx, big.NewInt(int64(blockNumber)))
	if err != nil {
		return 0, fmt.Errorf("failed to get block header: %w", err)
	}
	return header.Time, nil
}

// Multicall executes multiple calls in a single transaction
func (c *Client) Multicall(ctx context.Context, calls []Call) ([]Result, error) {
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
	data, err := c.multicallABI.Pack("aggregate3", multicallCalls)
	if err != nil {
		return nil, fmt.Errorf("failed to pack aggregate3 call: %w", err)
	}

	// Make the call
	msg := ethereum.CallMsg{
		To:   &c.multicallAddr,
		Data: data,
	}
	result, err := c.ethClient.CallContract(ctx, msg, nil)
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

// EncodeGetReservesCall encodes the getReserves function call
func (c *Client) EncodeGetReservesCall(pairAddr common.Address) (Call, error) {
	callData, err := c.pairABI.Pack("getReserves")
	if err != nil {
		return Call{}, fmt.Errorf("failed to pack getReserves call: %w", err)
	}

	return Call{
		Target:   pairAddr,
		CallData: callData,
	}, nil
}

// DecodeGetReservesResult decodes the result of a getReserves call
func (c *Client) DecodeGetReservesResult(result Result) (*big.Int, *big.Int, error) {
	if !result.Success {
		return nil, nil, fmt.Errorf("getReserves call failed")
	}

	results, err := c.pairABI.Unpack("getReserves", result.ReturnData)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to unpack getReserves result: %w", err)
	}

	if len(results) < 2 {
		return nil, nil, fmt.Errorf("insufficient results")
	}

	reserve0, ok := results[0].(*big.Int)
	if !ok {
		return nil, nil, fmt.Errorf("invalid reserve0 type")
	}

	reserve1, ok := results[1].(*big.Int)
	if !ok {
		return nil, nil, fmt.Errorf("invalid reserve1 type")
	}

	return reserve0, reserve1, nil
}

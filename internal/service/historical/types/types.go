package types

import (
	"context"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
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

// EthClient defines the interface for Ethereum client operations
type EthClient interface {
	// Multicall operations
	Multicall(ctx context.Context, blockNumber uint64, calls []Call) ([]Result, error)
	TryAggregate(ctx context.Context, requireSuccess bool, calls []Call) ([]Result, error)
	EncodeGetReservesCall(pairAddress common.Address) ([]byte, error)
	DecodeGetReservesResult(data []byte) (reserve0, reserve1 *big.Int, blockTimestampLast uint32, err error)
	GetBlockTimes(ctx context.Context, blockNumbers []uint64) (map[uint64]time.Time, error)
	GetCurrentBlock(ctx context.Context) (uint64, error)
	FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error)
	CallContract(ctx context.Context, call ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
	// Additional ContractBackend methods
	CodeAt(ctx context.Context, contract common.Address, blockNumber *big.Int) ([]byte, error)
	PendingCodeAt(ctx context.Context, account common.Address) ([]byte, error)
	PendingNonceAt(ctx context.Context, account common.Address) (uint64, error)
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
	EstimateGas(ctx context.Context, call ethereum.CallMsg) (uint64, error)
	SendTransaction(ctx context.Context, tx *types.Transaction) error
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)
	SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error)
	SuggestGasTipCap(ctx context.Context) (*big.Int, error)
	// ERC20 methods
	EncodeERC20Call(method string, args []interface{}) ([]byte, error)
	DecodeERC20Result(method string, data []byte) (interface{}, error)
}

// BackfillDB defines the database interface needed for backfill operations
type BackfillDB interface {
	InsertPairPrice(ctx context.Context, pairAddress []byte, blockNumber int64, blockTime time.Time,
		reserves0, reserves1, price0USD, price1USD, tvlUSD, confidence float64) error
	UpdatePairProgress(ctx context.Context, pairAddress []byte, lastProcessedBlock uint64) error
	GetPairProgress(ctx context.Context, pairAddress []byte) (uint64, error)
	GetPairsForBackfill(ctx context.Context, limit int) ([]common.Address, error)
	InsertToken(ctx context.Context, address []byte, symbol string, decimals int, tokenType string) error
	InsertPair(ctx context.Context, address, token0, token1 []byte, createdAtBlock int64) error
}

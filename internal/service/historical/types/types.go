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

// EthClient defines the interface for interacting with Ethereum
type EthClient interface {
	// Multicall operations
	Multicall(ctx context.Context, blockNumber uint64, calls []Call) ([]Result, error)
	TryAggregate(ctx context.Context, requireSuccess bool, calls []Call) ([]Result, error)
	EncodeGetReservesCall(pairAddr common.Address) ([]byte, error)
	DecodeGetReservesResult(data []byte) (*big.Int, *big.Int, uint32, error)
	GetBlockTimes(ctx context.Context, blockNumbers []uint64) (map[uint64]time.Time, error)
	GetCurrentBlock(ctx context.Context) (uint64, error)
	FilterLogs(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error)
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
	GetPairTokens(ctx context.Context, pairAddr common.Address) (token0, token1 common.Address, err error)
	GetTokenDecimals(ctx context.Context, tokenAddr common.Address) (uint8, error)
	GetTokensDecimals(ctx context.Context, tokenAddrs []common.Address) ([]uint8, error)
	GetTokenMetadata(ctx context.Context, tokenAddr common.Address) (*TokenMetadata, error)
	GetTokensMetadata(ctx context.Context, tokenAddrs []common.Address) ([]*TokenMetadata, error)
}

// Token represents a token in the database
type Token struct {
	Symbol   string
	Name     string
	Decimals int
}

// BackfillDB defines the database interface for historical data collection
type BackfillDB interface {
	GetPairProgress(ctx context.Context, pairAddress []byte) (uint64, error)
	UpdatePairProgress(ctx context.Context, pairAddress []byte, lastProcessedBlock uint64) error
	GetGlobalSyncProgress(ctx context.Context) (uint64, error)
	UpdateGlobalSyncProgress(ctx context.Context, lastProcessedBlock uint64) error
	GetPairsForBackfill(ctx context.Context, limit int) ([]common.Address, error)
	InsertToken(ctx context.Context, address []byte, name string, symbol string, decimals int, tokenType string) error
	InsertPair(ctx context.Context, address, token0, token1 []byte, createdAtBlock int64) error
	InsertPairPrice(ctx context.Context, pairAddress []byte, blockNumber int64, blockTime time.Time,
		reserves0, reserves1 float64, price0Usd, price1Usd, tvlUsd, confidence float64) error
	GetPair(ctx context.Context, pairAddress []byte) (*Pair, error)
	GetToken(ctx context.Context, address []byte) (*Token, error)
}

// Pair represents a trading pair
type Pair struct {
	Address        []byte
	Token0Address  []byte
	Token1Address  []byte
	CreatedAtBlock int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Config holds all configuration for historical data collection
type Config struct {
	MaxRetries         int
	RetryDelay         time.Duration
	MaxConcurrentPairs int
	BlockRange         uint64
	RateLimit          time.Duration
}

func DefaultConfig() Config {
	return Config{
		MaxRetries:         5,
		RetryDelay:         time.Second * 5,
		MaxConcurrentPairs: 10,
		BlockRange:         50000,
		RateLimit:          time.Millisecond * 200,
	}
}

// TokenMetadata represents the metadata for a token
type TokenMetadata struct {
	Name     string
	Symbol   string
	Decimals uint8
}

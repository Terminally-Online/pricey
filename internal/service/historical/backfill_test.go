package historical

import (
	"context"
	"math/big"
	"strings"
	"testing"
	"time"

	"pricey/internal/service/historical/types"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
)

type MockEthClient struct {
	mock.Mock
}

func (m *MockEthClient) Multicall(ctx context.Context, blockNumber uint64, calls []types.Call) ([]types.Result, error) {
	args := m.Called(ctx, blockNumber, calls)
	return args.Get(0).([]types.Result), args.Error(1)
}

func (m *MockEthClient) EncodeGetReservesCall(pairAddress common.Address) ([]byte, error) {
	args := m.Called(pairAddress)
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockEthClient) DecodeGetReservesResult(data []byte) (*big.Int, *big.Int, uint32, error) {
	args := m.Called(data)
	return args.Get(0).(*big.Int), args.Get(1).(*big.Int), args.Get(2).(uint32), args.Error(3)
}

func (m *MockEthClient) GetBlockTimes(ctx context.Context, blockNumbers []uint64) (map[uint64]time.Time, error) {
	args := m.Called(ctx, blockNumbers)
	return args.Get(0).(map[uint64]time.Time), args.Error(1)
}

func (m *MockEthClient) GetCurrentBlock(ctx context.Context) (uint64, error) {
	args := m.Called(ctx)
	return args.Get(0).(uint64), args.Error(1)
}

func (m *MockEthClient) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]ethtypes.Log, error) {
	args := m.Called(ctx, q)
	return args.Get(0).([]ethtypes.Log), args.Error(1)
}

func (m *MockEthClient) CallContract(ctx context.Context, call ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	args := m.Called(ctx, call, blockNumber)
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockEthClient) CodeAt(ctx context.Context, contract common.Address, blockNumber *big.Int) ([]byte, error) {
	args := m.Called(ctx, contract, blockNumber)
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockEthClient) PendingCodeAt(ctx context.Context, account common.Address) ([]byte, error) {
	args := m.Called(ctx, account)
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockEthClient) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	args := m.Called(ctx, account)
	return args.Get(0).(uint64), args.Error(1)
}

func (m *MockEthClient) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	args := m.Called(ctx)
	return args.Get(0).(*big.Int), args.Error(1)
}

func (m *MockEthClient) EstimateGas(ctx context.Context, call ethereum.CallMsg) (uint64, error) {
	args := m.Called(ctx, call)
	return args.Get(0).(uint64), args.Error(1)
}

func (m *MockEthClient) SendTransaction(ctx context.Context, tx *ethtypes.Transaction) error {
	args := m.Called(ctx, tx)
	return args.Error(0)
}

func (m *MockEthClient) HeaderByNumber(ctx context.Context, number *big.Int) (*ethtypes.Header, error) {
	args := m.Called(ctx, number)
	return args.Get(0).(*ethtypes.Header), args.Error(1)
}

func (m *MockEthClient) SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- ethtypes.Log) (ethereum.Subscription, error) {
	args := m.Called(ctx, q, ch)
	return args.Get(0).(ethereum.Subscription), args.Error(1)
}

func (m *MockEthClient) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	args := m.Called(ctx)
	return args.Get(0).(*big.Int), args.Error(1)
}

func (m *MockEthClient) TryAggregate(ctx context.Context, requireSuccess bool, calls []types.Call) ([]types.Result, error) {
	args := m.Called(ctx, requireSuccess, calls)
	return args.Get(0).([]types.Result), args.Error(1)
}

func (m *MockEthClient) EncodeERC20Call(method string, args []interface{}) ([]byte, error) {
	mockArgs := m.Called(method, args)
	return mockArgs.Get(0).([]byte), mockArgs.Error(1)
}

func (m *MockEthClient) DecodeERC20Result(method string, data []byte) (interface{}, error) {
	args := m.Called(method, data)
	return args.Get(0), args.Error(1)
}

type MockDB struct {
	mock.Mock
}

func (m *MockDB) InsertPairPrice(ctx context.Context, pairAddress []byte, blockNumber int64, blockTime time.Time,
	reserves0, reserves1, price0USD, price1USD, tvlUSD, confidence float64) error {
	args := m.Called(ctx, pairAddress, blockNumber, blockTime, reserves0, reserves1, price0USD, price1USD, tvlUSD, confidence)
	return args.Error(0)
}

func (m *MockDB) UpdatePairProgress(ctx context.Context, pairAddress []byte, lastProcessedBlock uint64) error {
	args := m.Called(ctx, pairAddress, lastProcessedBlock)
	return args.Error(0)
}

func (m *MockDB) GetPairProgress(ctx context.Context, pairAddress []byte) (uint64, error) {
	args := m.Called(ctx, pairAddress)
	return args.Get(0).(uint64), args.Error(1)
}

func (m *MockDB) GetPairsForBackfill(ctx context.Context, limit int) ([]common.Address, error) {
	args := m.Called(ctx, limit)
	return args.Get(0).([]common.Address), args.Error(1)
}

func (m *MockDB) InsertToken(ctx context.Context, address []byte, name string, symbol string, decimals int, tokenType string) error {
	args := m.Called(ctx, address, name, symbol, decimals, tokenType)
	return args.Error(0)
}

func (m *MockDB) InsertPair(ctx context.Context, address, token0, token1 []byte, createdAtBlock int64) error {
	args := m.Called(ctx, address, token0, token1, createdAtBlock)
	return args.Error(0)
}

func TestBackfillManager(t *testing.T) {
	mockDB := new(MockDB)
	mockEth := new(MockEthClient)
	config := DefaultConfig()
	config.BatchSize = 1

	// Setup mocks for pair discovery
	logs := []ethtypes.Log{
		{
			Address: UniswapV2Factory,
			Topics: []common.Hash{
				PairCreatedTopic,
				common.HexToHash("0x000000000000000000000000a0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"), // USDC
				common.HexToHash("0x000000000000000000000000c02aaa39b223fe8d0a0e5c4f27ead9083c756cc2"), // WETH
			},
			Data:        common.FromHex("0x000000000000000000000000b4e16d0168e52d35cacd2c6185b44281ec28c9dc0000000000000000000000000000000000000000000000000000000000000001"), // Pair address + allPairsLength
			BlockNumber: 10000000,
		},
	}
	mockEth.On("FilterLogs", mock.Anything, mock.Anything).Return(logs, nil)

	// Setup mocks for token metadata
	// USDC metadata
	mockEth.On("CodeAt", mock.Anything, mock.MatchedBy(func(addr common.Address) bool {
		return addr.Hex() == "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	}), mock.Anything).Return([]byte{1}, nil)
	mockEth.On("EncodeERC20Call", "symbol", mock.Anything).Return([]byte{1}, nil)
	mockEth.On("EncodeERC20Call", "decimals", mock.Anything).Return([]byte{1}, nil)
	mockEth.On("DecodeERC20Result", "symbol", mock.Anything).Return("USDC", nil)
	mockEth.On("DecodeERC20Result", "decimals", mock.Anything).Return(uint8(6), nil)

	// WETH metadata
	mockEth.On("CodeAt", mock.Anything, mock.MatchedBy(func(addr common.Address) bool {
		return addr.Hex() == "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"
	}), mock.Anything).Return([]byte{1}, nil)
	mockEth.On("EncodeERC20Call", "symbol", mock.Anything).Return([]byte{1}, nil)
	mockEth.On("EncodeERC20Call", "decimals", mock.Anything).Return([]byte{1}, nil)
	mockEth.On("DecodeERC20Result", "symbol", mock.Anything).Return("WETH", nil)
	mockEth.On("DecodeERC20Result", "decimals", mock.Anything).Return(uint8(18), nil)

	// Setup mocks for price collection
	reserves0 := big.NewInt(1000)
	reserves1 := big.NewInt(1000)
	mockEth.On("EncodeGetReservesCall", mock.Anything).Return([]byte{1, 2, 3}, nil)
	mockEth.On("DecodeGetReservesResult", mock.Anything).Return(reserves0, reserves1, uint32(0), nil)

	results := make([]types.Result, 100)
	for i := range results {
		results[i] = types.Result{Success: true, ReturnData: []byte{1, 2, 3}}
	}
	mockEth.On("Multicall", mock.Anything, mock.Anything, mock.Anything).Return(results, nil)

	blockTimes := make(map[uint64]time.Time)
	for i := uint64(0); i < 100; i++ {
		blockTimes[i] = time.Now()
	}
	mockEth.On("GetBlockTimes", mock.Anything, mock.Anything).Return(blockTimes, nil)
	mockEth.On("GetCurrentBlock", mock.Anything).Return(uint64(10001000), nil)

	// Setup DB mocks
	mockDB.On("InsertToken", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockDB.On("InsertPair", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	pairs := []common.Address{common.HexToAddress("0xb4e16d0168e52d35cacd2c6185b44281ec28c9dc")}
	mockDB.On("GetPairsForBackfill", mock.Anything, mock.Anything).Return(pairs, nil)
	mockDB.On("GetPairProgress", mock.Anything, mock.Anything).Return(uint64(10000000), nil)
	mockDB.On("UpdatePairProgress", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockDB.On("InsertPairPrice", mock.Anything, mock.Anything, mock.Anything, mock.Anything,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	manager := NewBackfillManager(mockDB, mockEth, config)

	// Run backfill with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := manager.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Errorf("expected error containing deadline exceeded, got %v", err)
	}
}

package uniswap

import (
	"context"
	"math/big"
	"testing"
	"time"

	bindings "pricey/contracts/bindings/uniswap_pair"
	"pricey/pkg/config"
	ptypes "pricey/pkg/types"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/event"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockEthClient mocks the Ethereum client
type MockEthClient struct {
	mock.Mock
}

func (m *MockEthClient) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	args := m.Called(ctx, number)
	return args.Get(0).(*types.Header), args.Error(1)
}

func (m *MockEthClient) CodeAt(ctx context.Context, contract common.Address, blockNumber *big.Int) ([]byte, error) {
	args := m.Called(ctx, contract, blockNumber)
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockEthClient) CallContract(ctx context.Context, call ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	args := m.Called(ctx, call, blockNumber)
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

func (m *MockEthClient) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	args := m.Called(ctx, tx)
	return args.Error(0)
}

func (m *MockEthClient) FilterLogs(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	args := m.Called(ctx, query)
	return args.Get(0).([]types.Log), args.Error(1)
}

func (m *MockEthClient) SubscribeFilterLogs(ctx context.Context, query ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
	args := m.Called(ctx, query, ch)
	return args.Get(0).(ethereum.Subscription), args.Error(1)
}

func (m *MockEthClient) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	args := m.Called(ctx)
	return args.Get(0).(*big.Int), args.Error(1)
}

// MockBindings mocks the Uniswap pair contract bindings
type MockBindings struct {
	mock.Mock
}

var _ PairBindings = (*MockBindings)(nil) // Verify MockBindings implements PairBindings

func (m *MockBindings) Token0(opts *bind.CallOpts) (common.Address, error) {
	args := m.Called(opts)
	return args.Get(0).(common.Address), args.Error(1)
}

func (m *MockBindings) Token1(opts *bind.CallOpts) (common.Address, error) {
	args := m.Called(opts)
	return args.Get(0).(common.Address), args.Error(1)
}

func (m *MockBindings) GetReserves(opts *bind.CallOpts) (struct {
	Reserve0           *big.Int
	Reserve1           *big.Int
	BlockTimestampLast uint32
}, error) {
	args := m.Called(opts)
	return args.Get(0).(struct {
		Reserve0           *big.Int
		Reserve1           *big.Int
		BlockTimestampLast uint32
	}), args.Error(1)
}

func (m *MockBindings) WatchSync(opts *bind.WatchOpts, sink chan<- *bindings.BindingsSync) (event.Subscription, error) {
	args := m.Called(opts, sink)
	return args.Get(0).(event.Subscription), args.Error(1)
}

// MockSubscription mocks the event subscription
type MockSubscription struct {
	mock.Mock
	errChan chan error
}

func (m *MockSubscription) Unsubscribe() {
	m.Called()
}

func (m *MockSubscription) Err() <-chan error {
	return m.errChan
}

// MockPair is a mock implementation of the Pair interface
type MockPair struct {
	mock.Mock
}

func (m *MockPair) Token0(opts *bind.CallOpts) (common.Address, error) {
	args := m.Called()
	return args.Get(0).(common.Address), args.Error(1)
}

func (m *MockPair) Token1(opts *bind.CallOpts) (common.Address, error) {
	args := m.Called()
	return args.Get(0).(common.Address), args.Error(1)
}

func (m *MockPair) GetReserves(opts *bind.CallOpts) (struct {
	Reserve0           *big.Int
	Reserve1           *big.Int
	BlockTimestampLast uint32
}, error) {
	args := m.Called(opts)
	return args.Get(0).(struct {
		Reserve0           *big.Int
		Reserve1           *big.Int
		BlockTimestampLast uint32
	}), args.Error(1)
}

func (m *MockPair) WatchSync(opts *bind.WatchOpts, sink chan<- *bindings.BindingsSync) (event.Subscription, error) {
	args := m.Called(opts, sink)
	return args.Get(0).(event.Subscription), args.Error(1)
}

func setupMockPair() (*MockEthClient, *MockPair, *MockSubscription, common.Address, common.Address, common.Address) {
	// Define test addresses
	token0 := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")   // USDC
	token1 := common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2")   // WETH
	pairAddr := common.HexToAddress("0xB4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc") // USDC/WETH pair

	mockClient := new(MockEthClient)
	mockPair := new(MockPair)
	mockSub := new(MockSubscription)

	// Set up mock expectations
	mockSub.On("Unsubscribe").Return()
	mockClient.On("SubscribeFilterLogs", mock.Anything, mock.Anything, mock.Anything).Return(mockSub, nil)
	mockClient.On("FilterLogs", mock.Anything, mock.Anything).Return([]types.Log{}, nil)

	// Mock HeaderByNumber
	mockClient.On("HeaderByNumber", mock.Anything, mock.Anything).Return(&types.Header{
		Time:   uint64(time.Now().Unix()),
		Number: big.NewInt(12345),
	}, nil)

	// Mock contract calls
	mockClient.On("CallContract", mock.Anything, mock.MatchedBy(func(call ethereum.CallMsg) bool {
		return len(call.Data) >= 4 && string(call.Data[:4]) == "\x0d\xfe\x16\x81" // token0()
	}), mock.Anything).Return(common.LeftPadBytes(token0.Bytes(), 32), nil)

	mockClient.On("CallContract", mock.Anything, mock.MatchedBy(func(call ethereum.CallMsg) bool {
		return len(call.Data) >= 4 && string(call.Data[:4]) == "\xd2\x1b\xee\x1d" // token1()
	}), mock.Anything).Return(common.LeftPadBytes(token1.Bytes(), 32), nil)

	// Mock getReserves call
	reserves0 := common.LeftPadBytes(big.NewInt(1000000).Bytes(), 32)
	reserves1 := common.LeftPadBytes(big.NewInt(500000).Bytes(), 32)
	timestamp := common.LeftPadBytes(big.NewInt(int64(time.Now().Unix())).Bytes(), 32)
	reservesData := append(append(reserves0, reserves1...), timestamp...)

	mockClient.On("CallContract", mock.Anything, mock.MatchedBy(func(call ethereum.CallMsg) bool {
		return len(call.Data) >= 4 && string(call.Data[:4]) == "\x0b\x02\xc6\xa3" // getReserves()
	}), mock.Anything).Return(reservesData, nil)

	mockClient.On("CodeAt", mock.Anything, pairAddr, mock.Anything).Return([]byte{1}, nil)

	// Set up mock pair
	mockPair.On("Token0", mock.Anything).Return(token0, nil)
	mockPair.On("Token1", mock.Anything).Return(token1, nil)
	mockPair.On("GetReserves", mock.Anything).Return(struct {
		Reserve0           *big.Int
		Reserve1           *big.Int
		BlockTimestampLast uint32
	}{
		Reserve0:           big.NewInt(1000000),
		Reserve1:           big.NewInt(500000),
		BlockTimestampLast: uint32(time.Now().Unix()),
	}, nil)

	// Mock WatchSync
	mockPair.On("WatchSync", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		ch := args.Get(1).(chan<- *bindings.BindingsSync)
		go func() {
			ch <- &bindings.BindingsSync{
				Reserve0: big.NewInt(1100000),
				Reserve1: big.NewInt(550000),
			}
		}()
	}).Return(mockSub, nil)

	return mockClient, mockPair, mockSub, token0, token1, pairAddr
}

func TestGetPrice(t *testing.T) {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		t.Skip("Skipping test due to missing configuration:", err)
	}

	client, err := ethclient.Dial(cfg.EthereumNodeURL)
	require.NoError(t, err)
	defer client.Close()

	service := NewService(client)

	tests := []struct {
		name       string
		pairAddr   common.Address
		blockNum   *big.Int // nil for latest
		wantErr    bool
		checkPrice func(*testing.T, *ptypes.Price)
	}{
		{
			name:     "USDC/ETH pair current price",
			pairAddr: common.HexToAddress("0xB4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc"),
			wantErr:  false,
			checkPrice: func(t *testing.T, p *ptypes.Price) {
				require.NotNil(t, p)
				require.True(t, p.Price0.Sign() > 0, "Price0 should be positive")
				require.True(t, p.Price1.Sign() > 0, "Price1 should be positive")
				require.NotZero(t, p.BlockNumber, "BlockNumber should not be zero")
				require.True(t, p.Timestamp.Before(time.Now()), "Timestamp should be in the past")
				require.NotNil(t, p.Reserves0, "Reserves0 should not be nil")
				require.NotNil(t, p.Reserves1, "Reserves1 should not be nil")
				require.True(t, p.Reserves0.Sign() > 0, "Reserves0 should be positive")
				require.True(t, p.Reserves1.Sign() > 0, "Reserves1 should be positive")
			},
		},
		{
			name:     "Invalid pair address",
			pairAddr: common.HexToAddress("0x0000000000000000000000000000000000000000"),
			wantErr:  true,
		},
		{
			// Test historical price at a specific block
			name:     "USDC/ETH historical price",
			pairAddr: common.HexToAddress("0xB4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc"),
			blockNum: big.NewInt(17000000), // Specific Ethereum block number
			wantErr:  false,
			checkPrice: func(t *testing.T, p *ptypes.Price) {
				require.NotNil(t, p)
				require.Equal(t, uint64(17000000), p.BlockNumber)
				require.True(t, p.Price0.Sign() > 0)
				require.True(t, p.Price1.Sign() > 0)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &ptypes.PriceOpts{BlockNumber: tt.blockNum}
			price, err := service.GetPrice(context.Background(), tt.pairAddr, opts)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			if tt.checkPrice != nil {
				tt.checkPrice(t, price)
			}
		})
	}
}

func TestSubscribeToPrice(t *testing.T) {
	mockClient, mockPair, _, _, _, pairAddr := setupMockPair()
	service := NewService(mockClient)
	service.pairs[pairAddr] = mockPair

	// Subscribe to price updates
	sub, err := service.SubscribeToPrice(context.Background(), pairAddr)
	require.NoError(t, err)
	require.NotNil(t, sub)

	// Verify initial price update
	update := <-sub.Updates
	require.NotNil(t, update)
	require.NotNil(t, update.Price)
	require.NoError(t, update.Error)
}

func TestMultipleSubscriptions(t *testing.T) {
	mockClient, mockPair, _, _, _, pairAddr := setupMockPair()
	service := NewService(mockClient)
	service.pairs[pairAddr] = mockPair

	// Create multiple subscribers
	sub1, err := service.SubscribeToPrice(context.Background(), pairAddr)
	require.NoError(t, err)
	require.NotNil(t, sub1)

	sub2, err := service.SubscribeToPrice(context.Background(), pairAddr)
	require.NoError(t, err)
	require.NotNil(t, sub2)

	// Verify both subscribers receive initial price
	update1 := <-sub1.Updates
	require.NotNil(t, update1)
	require.NotNil(t, update1.Price)
	require.NoError(t, update1.Error)

	update2 := <-sub2.Updates
	require.NotNil(t, update2)
	require.NotNil(t, update2.Price)
	require.NoError(t, update2.Error)
}

func TestSyncEvent(t *testing.T) {
	mockClient, mockPair, mockSub, _, _, pairAddr := setupMockPair()
	service := NewService(mockClient)
	service.pairs[pairAddr] = mockPair

	// Subscribe to price updates
	sub, err := service.SubscribeToPrice(context.Background(), pairAddr)
	require.NoError(t, err)
	require.NotNil(t, sub)

	// Verify initial price update
	update := <-sub.Updates
	require.NotNil(t, update)
	require.NotNil(t, update.Price)
	require.NoError(t, update.Error)

	// Simulate a Sync event
	syncEvent := types.Log{
		Address: pairAddr,
		Topics:  []common.Hash{common.HexToHash("0x1c411e9a96e071241c2f21f7726b17ae89e3cab4c78be50e062b03a9fffbbad1")},
		Data:    append(common.LeftPadBytes(big.NewInt(1100000).Bytes(), 32), common.LeftPadBytes(big.NewInt(550000).Bytes(), 32)...),
	}

	// Send the event through the subscription
	mockClient.On("SubscribeFilterLogs", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		ch := args.Get(2).(chan<- types.Log)
		go func() {
			ch <- syncEvent
		}()
	}).Return(mockSub, nil)

	// Verify the price update after sync
	update = <-sub.Updates
	require.NotNil(t, update)
	require.NotNil(t, update.Price)
	require.NoError(t, update.Error)
}

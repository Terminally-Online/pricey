package uniswap

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	bindings "pricey/contracts/bindings/uniswap_pair"
	ptypes "pricey/pkg/types"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
)

// EthClient defines the interface for Ethereum client operations
type EthClient interface {
	bind.ContractBackend
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error)
}

// PairBindings defines the interface for interacting with Uniswap pairs
type PairBindings interface {
	Token0(opts *bind.CallOpts) (common.Address, error)
	Token1(opts *bind.CallOpts) (common.Address, error)
	GetReserves(opts *bind.CallOpts) (struct {
		Reserve0           *big.Int
		Reserve1           *big.Int
		BlockTimestampLast uint32
	}, error)
	WatchSync(opts *bind.WatchOpts, sink chan<- *bindings.BindingsSync) (event.Subscription, error)
}

// PriceUpdate represents a real-time price update from a pair
type PriceUpdate struct {
	Price   *ptypes.Price
	Error   error
	Info    string
	Warning string
}

// PriceSubscription represents an active price subscription
type PriceSubscription struct {
	Pair    common.Address
	Updates chan PriceUpdate
	done    chan struct{}
	unsub   func()
}

// RetryConfig defines retry parameters for error recovery
type RetryConfig struct {
	MaxAttempts int
	BackoffMin  time.Duration
	BackoffMax  time.Duration
}

// DefaultRetryConfig provides default retry parameters
var DefaultRetryConfig = RetryConfig{
	MaxAttempts: 3,
	BackoffMin:  time.Second,
	BackoffMax:  time.Second * 30,
}

// Service manages Uniswap pair interactions
type Service struct {
	client      EthClient
	mu          sync.RWMutex
	pairs       map[common.Address]PairBindings
	subs        map[common.Address][]*PriceSubscription
	watchCancel context.CancelFunc
	retryConfig RetryConfig
}

// NewService creates a new Uniswap service
func NewService(client EthClient) *Service {
	return &Service{
		client:      client,
		pairs:       make(map[common.Address]PairBindings),
		subs:        make(map[common.Address][]*PriceSubscription),
		retryConfig: DefaultRetryConfig,
	}
}

// SubscribeToPrice subscribes to real-time price updates for a specific pair
func (s *Service) SubscribeToPrice(ctx context.Context, pairAddress common.Address) (*PriceSubscription, error) {
	pair, err := s.getPair(pairAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to get pair: %w", err)
	}

	// Create subscription
	sub := &PriceSubscription{
		Pair:    pairAddress,
		Updates: make(chan PriceUpdate, 100), // Buffer size of 100
		done:    make(chan struct{}),
	}

	// Add to subscriptions map
	s.mu.Lock()
	if s.subs[pairAddress] == nil {
		s.subs[pairAddress] = make([]*PriceSubscription, 0)
	}
	s.subs[pairAddress] = append(s.subs[pairAddress], sub)
	s.mu.Unlock()

	// Get initial price
	price, err := s.GetPrice(ctx, pairAddress, nil)
	if err != nil {
		s.mu.Lock()
		s.subs[pairAddress] = s.subs[pairAddress][:len(s.subs[pairAddress])-1]
		s.mu.Unlock()
		return nil, fmt.Errorf("failed to get initial price: %w", err)
	}

	// Send initial price update
	select {
	case sub.Updates <- PriceUpdate{Price: price}:
	default:
		// Skip if channel is full
	}

	// Start watching for Sync events if this is the first subscription for this pair
	if len(s.subs[pairAddress]) == 1 {
		watchCtx, cancel := context.WithCancel(context.Background())
		s.watchCancel = cancel

		syncCh := make(chan *bindings.BindingsSync)
		syncSub, err := pair.WatchSync(&bind.WatchOpts{Context: watchCtx}, syncCh)
		if err != nil {
			s.mu.Lock()
			s.subs[pairAddress] = s.subs[pairAddress][:len(s.subs[pairAddress])-1]
			s.mu.Unlock()
			return nil, fmt.Errorf("failed to watch sync events: %w", err)
		}

		// Handle sync events
		go s.handleSyncEvents(pairAddress, syncCh, syncSub)
	}

	// Set up unsubscribe function
	sub.unsub = func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		// Find and remove subscription
		subs := s.subs[pairAddress]
		for i, existingSub := range subs {
			if existingSub == sub {
				s.subs[pairAddress] = append(subs[:i], subs[i+1:]...)
				break
			}
		}

		// If no more subscriptions for this pair, stop watching
		if len(s.subs[pairAddress]) == 0 {
			delete(s.subs, pairAddress)
			if s.watchCancel != nil {
				s.watchCancel()
			}
		}

		close(sub.Updates)
		close(sub.done)
	}

	return sub, nil
}

// handleSyncEvents processes Sync events and broadcasts price updates
func (s *Service) handleSyncEvents(pairAddress common.Address, syncCh chan *bindings.BindingsSync, syncSub event.Subscription) {
	defer syncSub.Unsubscribe()

	backoff := s.retryConfig.BackoffMin
	attempts := 0

	for {
		select {
		case err := <-syncSub.Err():
			if err == nil {
				continue
			}

			// Broadcast the error immediately
			s.broadcastError(pairAddress, fmt.Errorf("subscription error: %w", err))

			if attempts >= s.retryConfig.MaxAttempts {
				s.broadcastError(pairAddress, fmt.Errorf("max retry attempts reached: %w", err))
				return
			}

			// Exponential backoff
			time.Sleep(backoff)
			backoff *= 2
			if backoff > s.retryConfig.BackoffMax {
				backoff = s.retryConfig.BackoffMax
			}
			attempts++

			// Attempt to resubscribe
			pair, err := s.getPair(pairAddress)
			if err != nil {
				s.broadcastError(pairAddress, fmt.Errorf("failed to get pair during retry: %w", err))
				continue
			}

			newSyncCh := make(chan *bindings.BindingsSync, 10)
			newSyncSub, err := pair.WatchSync(&bind.WatchOpts{}, newSyncCh)
			if err != nil {
				s.broadcastError(pairAddress, fmt.Errorf("failed to resubscribe during retry: %w", err))
				continue
			}

			// Update channels and continue monitoring
			syncCh = newSyncCh
			syncSub = newSyncSub
			s.broadcastInfo(pairAddress, fmt.Sprintf("Successfully resubscribed after %d attempts", attempts))

		case sync := <-syncCh:
			if sync == nil {
				continue
			}

			// Reset retry counters on successful sync
			backoff = s.retryConfig.BackoffMin
			attempts = 0

			// Get latest price with retries
			var price *ptypes.Price
			var err error
			for i := 0; i < s.retryConfig.MaxAttempts; i++ {
				price, err = s.GetPrice(context.Background(), pairAddress, nil)
				if err == nil {
					break
				}
				time.Sleep(backoff)
				backoff *= 2
				if backoff > s.retryConfig.BackoffMax {
					backoff = s.retryConfig.BackoffMax
				}
			}

			if err != nil {
				s.broadcastError(pairAddress, fmt.Errorf("failed to get price after retries: %w", err))
				continue
			}

			// Broadcast update to all subscribers
			s.mu.RLock()
			subs := s.subs[pairAddress]
			s.mu.RUnlock()

			update := PriceUpdate{Price: price}
			for _, sub := range subs {
				select {
				case sub.Updates <- update:
				default:
					// Log dropped update
					s.broadcastWarning(pairAddress, "Dropped price update due to full channel")
				}
			}
		}
	}
}

// broadcastInfo sends an informational message to all subscribers
func (s *Service) broadcastInfo(pairAddress common.Address, msg string) {
	s.mu.RLock()
	subs := s.subs[pairAddress]
	s.mu.RUnlock()

	update := PriceUpdate{
		Price: nil,
		Error: nil,
		Info:  msg,
	}

	for _, sub := range subs {
		select {
		case sub.Updates <- update:
		default:
		}
	}
}

// broadcastWarning sends a warning message to all subscribers
func (s *Service) broadcastWarning(pairAddress common.Address, msg string) {
	s.mu.RLock()
	subs := s.subs[pairAddress]
	s.mu.RUnlock()

	update := PriceUpdate{
		Price:   nil,
		Error:   nil,
		Warning: msg,
	}

	for _, sub := range subs {
		select {
		case sub.Updates <- update:
		default:
		}
	}
}

// broadcastError sends an error to all subscribers of a pair
func (s *Service) broadcastError(pairAddress common.Address, err error) {
	s.mu.RLock()
	subs := s.subs[pairAddress]
	s.mu.RUnlock()

	update := PriceUpdate{Error: err}
	for _, sub := range subs {
		select {
		case sub.Updates <- update:
		default:
			// Skip if channel is full
		}
	}
}

// Unsubscribe cancels a price subscription
func (s *PriceSubscription) Unsubscribe() {
	if s.unsub != nil {
		s.unsub()
	}
}

// GetPrice retrieves the current or historical price for a pair
func (s *Service) GetPrice(ctx context.Context, pairAddress common.Address, opts *ptypes.PriceOpts) (*ptypes.Price, error) {
	var price *ptypes.Price
	var err error
	backoff := s.retryConfig.BackoffMin

	for i := 0; i < s.retryConfig.MaxAttempts; i++ {
		price, err = s.getPriceInternal(ctx, pairAddress, opts)
		if err == nil {
			return price, nil
		}

		// Check if context is cancelled before retrying
		if ctx.Err() != nil {
			return nil, fmt.Errorf("context cancelled during price retrieval: %w", ctx.Err())
		}

		// Wait before retrying
		time.Sleep(backoff)
		backoff *= 2
		if backoff > s.retryConfig.BackoffMax {
			backoff = s.retryConfig.BackoffMax
		}
	}

	return nil, fmt.Errorf("max retry attempts reached: %w", err)
}

// getPriceInternal is the internal implementation of GetPrice without retries
func (s *Service) getPriceInternal(ctx context.Context, pairAddress common.Address, opts *ptypes.PriceOpts) (*ptypes.Price, error) {
	pair, err := s.getPair(pairAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to get pair: %w", err)
	}

	// Get token addresses
	token0, err := pair.Token0(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get token0: %w", err)
	}

	token1, err := pair.Token1(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get token1: %w", err)
	}

	// Get reserves
	callOpts := &bind.CallOpts{Context: ctx}
	if opts != nil && opts.BlockNumber != nil {
		callOpts.BlockNumber = opts.BlockNumber
	}

	reserves, err := pair.GetReserves(callOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to get reserves: %w", err)
	}

	// Calculate prices
	price0 := new(big.Float).Quo(
		new(big.Float).SetInt(reserves.Reserve1),
		new(big.Float).SetInt(reserves.Reserve0),
	)

	price1 := new(big.Float).Quo(
		new(big.Float).SetInt(reserves.Reserve0),
		new(big.Float).SetInt(reserves.Reserve1),
	)

	// Calculate TVL (in terms of token1)
	tvl := new(big.Float).Mul(
		new(big.Float).SetInt(reserves.Reserve0),
		price0,
	)
	tvl.Add(tvl, new(big.Float).SetInt(reserves.Reserve1))

	var blockNumber *big.Int
	if opts != nil {
		blockNumber = opts.GetBlockNumber()
	}

	header, err := s.client.HeaderByNumber(ctx, blockNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to get block header: %w", err)
	}

	return &ptypes.Price{
		Token0Address: token0.Hex(),
		Token1Address: token1.Hex(),
		Price0:        price0,
		Price1:        price1,
		Timestamp:     time.Unix(int64(header.Time), 0),
		BlockNumber:   header.Number.Uint64(),
		Reserves0:     reserves.Reserve0,
		Reserves1:     reserves.Reserve1,
		TVL:           tvl,
	}, nil
}

func (s *Service) getPair(address common.Address) (PairBindings, error) {
	s.mu.RLock()
	pair, exists := s.pairs[address]
	s.mu.RUnlock()

	if exists {
		return pair, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock
	if pair, exists = s.pairs[address]; exists {
		return pair, nil
	}

	pair, err := bindings.NewBindings(address, s.client)
	if err != nil {
		return nil, err
	}

	s.pairs[address] = pair
	return pair, nil
}

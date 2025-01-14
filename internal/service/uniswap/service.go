package uniswap

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	bindings "pricey/contracts/bindings/uniswap_pair"
	"pricey/pkg/types"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

type Service struct {
	client *ethclient.Client
	mu     sync.RWMutex
	pairs  map[common.Address]*bindings.Bindings
}

func NewService(client *ethclient.Client) *Service {
	return &Service{
		client: client,
		pairs:  make(map[common.Address]*bindings.Bindings),
	}
}

func (s *Service) GetPrice(ctx context.Context, pairAddress common.Address, opts *types.PriceOpts) (*types.Price, error) {
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

	header, err := s.client.HeaderByNumber(ctx, opts.GetBlockNumber())
	if err != nil {
		return nil, fmt.Errorf("failed to get block header: %w", err)
	}

	return &types.Price{
		Token0Address: token0.Hex(),
		Token1Address: token1.Hex(),
		Price0:        price0,
		Price1:        price1,
		Timestamp:     time.Unix(int64(header.Time), 0),
		BlockNumber:   header.Number.Uint64(),
		Reserves0:     reserves.Reserve0,
		Reserves1:     reserves.Reserve1,
	}, nil
}

func (s *Service) getPair(address common.Address) (*bindings.Bindings, error) {
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

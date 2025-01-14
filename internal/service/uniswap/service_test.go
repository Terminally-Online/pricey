package uniswap

import (
	"context"
	"math/big"
	"testing"
	"time"

	"pricey/pkg/config"
	"pricey/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stretchr/testify/require"
)

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
		checkPrice func(*testing.T, *types.Price)
	}{
		{
			name:     "USDC/ETH pair current price",
			pairAddr: common.HexToAddress("0xB4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc"),
			wantErr:  false,
			checkPrice: func(t *testing.T, p *types.Price) {
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
			checkPrice: func(t *testing.T, p *types.Price) {
				require.NotNil(t, p)
				require.Equal(t, uint64(17000000), p.BlockNumber)
				require.True(t, p.Price0.Sign() > 0)
				require.True(t, p.Price1.Sign() > 0)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &types.PriceOpts{BlockNumber: tt.blockNum}
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

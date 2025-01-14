package price

import (
	"context"
	"math"
	"math/big"
	"testing"

	"pricey/internal/service/uniswap"
	"pricey/pkg/config"
	"pricey/pkg/types"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stretchr/testify/require"
)

func TestGetUSDPrice(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Skip("Skipping test due to missing configuration:", err)
	}

	client, err := ethclient.Dial(cfg.EthereumNodeURL)
	require.NoError(t, err)
	defer client.Close()

	uniswapService := uniswap.NewService(client)
	calculator := NewCalculator(uniswapService)

	tests := []struct {
		name       string
		tokenAddr  string
		wantErr    bool
		checkPrice func(*testing.T, *types.Price)
	}{
		{
			name:      "ETH price",
			tokenAddr: WETHAddress,
			checkPrice: func(t *testing.T, p *types.Price) {
				require.NotNil(t, p)
				require.NotNil(t, p.PriceUSD)

				// Convert price to float64 for easier comparison
				priceFloat, _ := p.PriceUSD.Float64()

				// ETH should be between $100 and $100,000
				require.Greater(t, priceFloat, float64(100), "ETH price too low")
				require.Less(t, priceFloat, float64(100000), "ETH price too high")

				// Get USDT price for comparison
				usdtPrice, err := calculator.GetUSDPrice(context.Background(), WETHAddress, nil)
				require.NoError(t, err)
				usdtFloat, _ := usdtPrice.PriceUSD.Float64()

				// Prices should be within 1% of each other
				priceDiff := math.Abs(priceFloat - usdtFloat)
				percentDiff := priceDiff / priceFloat
				require.Less(t, percentDiff, 0.01, "USDC and USDT prices differ by more than 1%%: USDC=%f, USDT=%f", priceFloat, usdtFloat)

				// Check reserves are significant
				reservesUSDC := new(big.Float).SetInt(p.Reserves1)
				minReserves := new(big.Float).SetFloat64(100000) // At least 100k USDC in pool
				require.True(t, reservesUSDC.Cmp(minReserves) > 0, "Pool reserves too low")

				// Verify path
				require.Equal(t, []string{WETHAddress, USDCAddress}, p.Path)

				// High confidence for direct pair
				require.InDelta(t, 1.0, p.Confidence, 0.001)
			},
		},
		{
			name:      "USDC/USDT price (sanity check)",
			tokenAddr: USDCAddress,
			checkPrice: func(t *testing.T, p *types.Price) {
				require.NotNil(t, p)
				require.NotNil(t, p.PriceUSD)

				priceFloat, _ := p.PriceUSD.Float64()
				// USDC should be very close to 1 USD
				require.InDelta(t, 1.0, priceFloat, 0.01, "USDC price should be ~1 USD")
			},
		},
		{
			name:      "Invalid token",
			tokenAddr: "0x0000000000000000000000000000000000000000",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			price, err := calculator.GetUSDPrice(context.Background(), tt.tokenAddr, nil)

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

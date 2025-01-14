package price

import (
	"context"
	"math/big"
	"testing"

	"pricey/internal/service/uniswap"
	"pricey/pkg/config"
	"pricey/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stretchr/testify/require"
)

// Common token addresses on Ethereum mainnet
var (
	WETH = common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2")
	USDC = common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	USDT = common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7")
	DAI  = common.HexToAddress("0x6B175474E89094C44Da98b954EedeAC495271d0F")
	UNI  = common.HexToAddress("0x1f9840a85d5aF5bf1D1762F925BDADdC4201F984")
)

// setupTestPairs initializes the graph with test pairs
func setupTestPairs(calculator *Calculator) {
	// WETH-USDC pair (high liquidity)
	wethReserve, _ := new(big.Int).SetString("10000000000000000000", 10) // 10 ETH
	usdcReserve, _ := new(big.Int).SetString("18000000000", 10)          // $18,000 USDC
	calculator.graph.AddPair(
		common.HexToAddress("0xB4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc"),
		WETH,
		USDC,
		18, // WETH decimals
		6,  // USDC decimals
		wethReserve,
		usdcReserve,
		new(big.Float).SetFloat64(36000000), // $36M TVL
	)

	// USDC-USDT pair (very high liquidity, stable pair)
	stableReserve, _ := new(big.Int).SetString("100000000000000", 10) // 100M USDC/USDT
	calculator.graph.AddPair(
		common.HexToAddress("0x3041CbD36888bECc7bbCBc0045E3B1f144466f5f"),
		USDC,
		USDT,
		6, // USDC decimals
		6, // USDT decimals
		stableReserve,
		stableReserve,
		new(big.Float).SetFloat64(200000000), // $200M TVL
	)

	// USDT-DAI pair (high liquidity, stable pair)
	usdtReserve, _ := new(big.Int).SetString("50000000000000", 10)            // 50M USDT
	daiReserve, _ := new(big.Int).SetString("50000000000000000000000000", 10) // 50M DAI
	calculator.graph.AddPair(
		common.HexToAddress("0xB20bd5D04BE54f870D5C0d3cA85d82b34B836405"),
		USDT,
		DAI,
		6,  // USDT decimals
		18, // DAI decimals
		usdtReserve,
		daiReserve,
		new(big.Float).SetFloat64(100000000), // $100M TVL
	)

	// UNI-WETH pair (medium liquidity)
	uniReserve, _ := new(big.Int).SetString("1000000000000000000000000", 10) // 1M UNI
	wethReserve2, _ := new(big.Int).SetString("500000000000000000000", 10)   // 500 ETH
	calculator.graph.AddPair(
		common.HexToAddress("0xd3d2E2692501A5c9Ca623199D38826e513033a17"),
		UNI,
		WETH,
		18, // UNI decimals
		18, // WETH decimals
		uniReserve,
		wethReserve2,
		new(big.Float).SetFloat64(1800000), // $1.8M TVL
	)
}

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
	setupTestPairs(calculator)

	tests := []struct {
		name       string
		tokenAddr  common.Address
		wantErr    bool
		checkPrice func(*testing.T, *types.Price)
	}{
		{
			name:      "ETH direct price through USDC",
			tokenAddr: WETH,
			checkPrice: func(t *testing.T, p *types.Price) {
				require.NotNil(t, p)
				require.NotNil(t, p.PriceUSD)

				// Convert price to float64 for easier comparison
				priceFloat, _ := p.PriceUSD.Float64()

				// ETH should be between $100 and $100,000
				require.Greater(t, priceFloat, float64(100), "ETH price too low")
				require.Less(t, priceFloat, float64(100000), "ETH price too high")

				// Check reserves are significant
				reservesUSDC := new(big.Float).SetInt(p.Reserves1)
				minReserves := new(big.Float).SetFloat64(100000) // At least 100k USDC in pool
				require.True(t, reservesUSDC.Cmp(minReserves) > 0, "Pool reserves too low")

				// High confidence for direct pair
				require.Greater(t, p.Confidence, 0.8, "Direct pair should have high confidence")
			},
		},
		{
			name:      "USDC/USDT stable pair price",
			tokenAddr: USDC,
			checkPrice: func(t *testing.T, p *types.Price) {
				require.NotNil(t, p)
				require.NotNil(t, p.PriceUSD)

				priceFloat, _ := p.PriceUSD.Float64()
				// USDC should be very close to 1 USD
				require.InDelta(t, 1.0, priceFloat, 0.01, "USDC price should be ~1 USD")
				require.Greater(t, p.Confidence, 0.9, "Stable pair should have very high confidence")
			},
		},
		{
			name:      "UNI indirect price through ETH",
			tokenAddr: UNI,
			checkPrice: func(t *testing.T, p *types.Price) {
				require.NotNil(t, p)
				require.NotNil(t, p.PriceUSD)

				priceFloat, _ := p.PriceUSD.Float64()
				// UNI should be between $0.10 and $1000
				require.Greater(t, priceFloat, 0.1, "UNI price too low")
				require.Less(t, priceFloat, 1000.0, "UNI price too high")

				// Check path length (should be 2 or 3 hops)
				require.True(t, len(p.Path) >= 3 && len(p.Path) <= 4, "Unexpected path length")

				// Lower confidence for indirect path
				require.True(t, p.Confidence > 0.3 && p.Confidence < 0.8,
					"Indirect path should have moderate confidence")

				// Verify TVL is tracked
				require.NotNil(t, p.TVL)
				tvlFloat, _ := p.TVL.Float64()
				require.Greater(t, tvlFloat, 10000.0, "TVL should be significant")
			},
		},
		{
			name:      "Invalid token",
			tokenAddr: common.Address{},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			price, err := calculator.GetUSDPrice(context.Background(), tt.tokenAddr.Hex(), nil)

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

// TestPriceConfidence tests the confidence calculation for different path scenarios
func TestPriceConfidence(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Skip("Skipping test due to missing configuration:", err)
	}

	client, err := ethclient.Dial(cfg.EthereumNodeURL)
	require.NoError(t, err)
	defer client.Close()

	uniswapService := uniswap.NewService(client)
	calculator := NewCalculator(uniswapService)
	setupTestPairs(calculator)

	// Test confidence scores for different paths
	tokens := []struct {
		name    string
		addr    common.Address
		minConf float64
		maxConf float64
		maxHops int
	}{
		{"WETH (direct)", WETH, 0.8, 1.0, 1},
		{"USDC (stable)", USDC, 0.9, 1.0, 1},
		{"UNI (indirect)", UNI, 0.3, 0.8, 3},
	}

	for _, token := range tokens {
		t.Run(token.name, func(t *testing.T) {
			price, err := calculator.GetUSDPrice(context.Background(), token.addr.Hex(), nil)
			require.NoError(t, err)
			require.NotNil(t, price)

			// Check confidence bounds
			require.Greater(t, price.Confidence, token.minConf,
				"Confidence too low for %s", token.name)
			require.Less(t, price.Confidence, token.maxConf,
				"Confidence too high for %s", token.name)

			// Check path length
			require.True(t, len(price.Path) <= token.maxHops+1,
				"Path too long for %s", token.name)

			// Verify TVL tracking
			require.NotNil(t, price.TVL)
			tvlFloat, _ := price.TVL.Float64()
			require.Greater(t, tvlFloat, 10000.0,
				"TVL too low for %s", token.name)
		})
	}
}

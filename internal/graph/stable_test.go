package graph

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStableTokenDetection(t *testing.T) {
	eth := common.HexToAddress("0x4")
	g := NewPairGraph(eth)

	// Create test tokens
	usdc := common.HexToAddress("0x1")
	usdt := common.HexToAddress("0x2")
	dai := common.HexToAddress("0x3")
	randomToken := common.HexToAddress("0x5")

	// Set up pairs with realistic reserves and liquidity
	t.Run("stable pair detection", func(t *testing.T) {
		// USDC/USDT pair with high liquidity and 1:1 ratio
		g.AddPair(
			common.HexToAddress("0xa1"),
			usdc, usdt,
			6, 6, // Both have 6 decimals
			big.NewInt(1000000000000), // 1M USDC
			big.NewInt(1002000000000), // 1.002M USDT (0.2% deviation)
			big.NewFloat(2000000),     // $2M liquidity
		)

		// USDC/DAI pair with high liquidity and near 1:1 ratio
		g.AddPair(
			common.HexToAddress("0xa2"),
			usdc, dai,
			6, 18, // USDC has 6, DAI has 18
			big.NewInt(500000000000),                                     // 500k USDC
			new(big.Int).Mul(big.NewInt(499000000000), big.NewInt(1e12)), // ~499k DAI
			big.NewFloat(1000000),                                        // $1M liquidity
		)

		// ETH/USDC pair (non-stable)
		ethAmount, _ := new(big.Int).SetString("100000000000000000000", 10) // 100 ETH
		g.AddPair(
			common.HexToAddress("0xa3"),
			eth, usdc,
			18, 6,
			ethAmount,
			big.NewInt(300000000000), // 300k USDC
			big.NewFloat(600000),     // $600k liquidity
		)

		// Random token pair with low liquidity
		tokenAmount, _ := new(big.Int).SetString("1000000000000000000000", 10) // 1000 tokens
		g.AddPair(
			common.HexToAddress("0xa4"),
			randomToken, usdc,
			18, 6,
			tokenAmount,
			big.NewInt(1000000000), // 1k USDC
			big.NewFloat(2000),     // $2k liquidity
		)

		// Test stable pair detection
		minLiquidity := big.NewFloat(100000) // $100k minimum liquidity
		maxVolatility := 0.02                // 2% maximum deviation

		// Test USDC/USDT stable relationship
		isStable := g.IsStablePair(usdc, usdt, minLiquidity, maxVolatility)
		assert.True(t, isStable, "USDC/USDT should be detected as stable")

		// Test USDC/DAI stable relationship
		isStable = g.IsStablePair(usdc, dai, minLiquidity, maxVolatility)
		assert.True(t, isStable, "USDC/DAI should be detected as stable")

		// Test ETH/USDC non-stable relationship
		isStable = g.IsStablePair(eth, usdc, minLiquidity, maxVolatility)
		assert.False(t, isStable, "ETH/USDC should not be detected as stable")

		// Test low liquidity pair
		isStable = g.IsStablePair(randomToken, usdc, minLiquidity, maxVolatility)
		assert.False(t, isStable, "Low liquidity pair should not be detected as stable")
	})

	t.Run("find stable tokens", func(t *testing.T) {
		minLiquidity := big.NewFloat(100000) // $100k minimum liquidity
		maxVolatility := 0.02                // 2% maximum deviation

		stableTokens := g.FindStableTokens(minLiquidity, maxVolatility)

		// Should find USDC, USDT, and DAI as stable tokens
		require.Contains(t, stableTokens, usdc, "USDC should be detected as stable")
		require.Contains(t, stableTokens, usdt, "USDT should be detected as stable")
		require.Contains(t, stableTokens, dai, "DAI should be detected as stable")

		// ETH and random token should not be detected as stable
		require.NotContains(t, stableTokens, eth, "ETH should not be detected as stable")
		require.NotContains(t, stableTokens, randomToken, "Random token should not be detected as stable")

		// Check stable relationships
		usdcInfo := stableTokens[usdc]
		require.GreaterOrEqual(t, len(usdcInfo.StablePairs), 2, "USDC should have at least 2 stable pairs")
		require.Contains(t, usdcInfo.StablePairs, usdt)
		require.Contains(t, usdcInfo.StablePairs, dai)
	})

	t.Run("get stable base pairs", func(t *testing.T) {
		minLiquidity := big.NewFloat(100000) // $100k minimum liquidity
		maxVolatility := 0.02                // 2% maximum deviation

		basePairs := g.GetStableBasePairs(minLiquidity, maxVolatility)
		require.NotEmpty(t, basePairs, "Should find stable base pairs")

		// First pair should be highest liquidity (USDC/USDT)
		highestLiqPair := basePairs[0]
		require.True(t,
			(highestLiqPair.Token0 == usdc && highestLiqPair.Token1 == usdt) ||
				(highestLiqPair.Token0 == usdt && highestLiqPair.Token1 == usdc),
			"Highest liquidity pair should be USDC/USDT")
	})
}

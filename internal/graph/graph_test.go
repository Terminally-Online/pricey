package graph

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestPriceGraph(t *testing.T) {
	// Setup test addresses
	weth := common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2")
	usdc := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	token := common.HexToAddress("0x1234567890123456789012345678901234567890")

	// Create graph with WETH and USDC as reference tokens
	graph := NewPriceGraph(weth, usdc)

	t.Run("direct pair", func(t *testing.T) {
		// Add WETH/USDC pair
		graph.AddPair(
			common.HexToAddress("0xB4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc"),
			weth,
			usdc,
			18,                 // WETH decimals
			6,                  // USDC decimals
			big.NewInt(1e18),   // 1 WETH
			big.NewInt(3000e6), // 3000 USDC
			big.NewFloat(6000), // $6000 TVL
		)

		// Find path from WETH to USDC
		path, confidence := graph.FindBestPath(weth, usdc)
		require.NotNil(t, path)
		require.Equal(t, 1, len(path))
		require.Equal(t, float64(1.0), confidence)
		require.Equal(t, weth, path[0].Token0)
		require.Equal(t, usdc, path[0].Token1)
	})

	t.Run("indirect pair through WETH", func(t *testing.T) {
		// Add TOKEN/WETH pair
		weiAmount, _ := new(big.Int).SetString("100000000000000000000", 10) // 100 * 10^18
		graph.AddPair(
			common.HexToAddress("0x9999999999999999999999999999999999999999"),
			token,
			weth,
			18,                 // TOKEN decimals
			18,                 // WETH decimals
			weiAmount,          // 100 TOKEN
			big.NewInt(1e18),   // 1 WETH
			big.NewFloat(3000), // $3000 TVL
		)

		// Find path from TOKEN to USDC
		path, confidence := graph.FindBestPath(token, usdc)
		require.NotNil(t, path)
		require.Equal(t, 2, len(path))
		require.Equal(t, float64(0.9), confidence) // 90% confidence for WETH routes

		// Verify path: TOKEN -> WETH -> USDC
		require.Equal(t, token, path[0].Token0)
		require.Equal(t, weth, path[0].Token1)
		require.Equal(t, weth, path[1].Token0)
		require.Equal(t, usdc, path[1].Token1)
	})

	t.Run("non-existent path", func(t *testing.T) {
		nonexistent := common.HexToAddress("0x0000000000000000000000000000000000000000")
		path, confidence := graph.FindBestPath(nonexistent, usdc)
		require.Nil(t, path)
		require.Equal(t, float64(0), confidence)
	})
}

func TestPriceGraphConcurrency(t *testing.T) {
	weth := common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2")
	usdc := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	graph := NewPriceGraph(weth, usdc)

	// Add initial pair
	graph.AddPair(
		common.HexToAddress("0xB4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc"),
		weth,
		usdc,
		18,
		6,
		big.NewInt(1e18),
		big.NewInt(3000e6),
		big.NewFloat(6000),
	)

	// Test concurrent reads
	t.Run("concurrent reads", func(t *testing.T) {
		done := make(chan bool)
		for i := 0; i < 100; i++ {
			go func() {
				path, _ := graph.FindBestPath(weth, usdc)
				require.NotNil(t, path)
				done <- true
			}()
		}

		// Wait for all goroutines
		for i := 0; i < 100; i++ {
			<-done
		}
	})

	// Test concurrent writes
	t.Run("concurrent writes", func(t *testing.T) {
		done := make(chan bool)
		for i := 0; i < 100; i++ {
			go func(i int) {
				token := common.BigToAddress(big.NewInt(int64(i)))
				graph.AddPair(
					token,
					token,
					weth,
					18,
					18,
					big.NewInt(1e18),
					big.NewInt(1e18),
					big.NewFloat(2000),
				)
				done <- true
			}(i)
		}

		// Wait for all goroutines
		for i := 0; i < 100; i++ {
			<-done
		}
	})
}

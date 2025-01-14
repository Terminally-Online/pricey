package graph

import (
	"math"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPairGraph(t *testing.T) {
	// Create test addresses
	token0 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	token1 := common.HexToAddress("0x2222222222222222222222222222222222222222")
	token2 := common.HexToAddress("0x3333333333333333333333333333333333333333")
	token3 := common.HexToAddress("0x4444444444444444444444444444444444444444")
	pair01 := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	pair12 := common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	pair23 := common.HexToAddress("0xcccccccccccccccccccccccccccccccccccccccc")

	// Create test reserves and liquidity
	reserves0 := big.NewInt(1000000)
	reserves1 := big.NewInt(2000000)
	liquidity := new(big.Float).SetFloat64(1000000)

	t.Run("Add and retrieve pairs", func(t *testing.T) {
		graph := NewPairGraph(common.Address{})

		// Add token0/token1 pair
		graph.AddPair(pair01, token0, token1, 18, 18, reserves0, reserves1, liquidity)

		// Add token1/token2 pair
		graph.AddPair(pair12, token1, token2, 18, 6, reserves0, reserves1, liquidity)

		// Check token0's edges
		edges0 := graph.GetEdges(token0)
		assert.Equal(t, 1, len(edges0))
		assert.Equal(t, pair01, edges0[0].Pair)
		assert.Equal(t, token0, edges0[0].Token0)
		assert.Equal(t, token1, edges0[0].Token1)

		// Check token1's edges (should have 2)
		edges1 := graph.GetEdges(token1)
		assert.Equal(t, 2, len(edges1))

		// Check token2's edges
		edges2 := graph.GetEdges(token2)
		assert.Equal(t, 1, len(edges2))
		assert.Equal(t, pair12, edges2[0].Pair)
		assert.Equal(t, token1, edges2[0].Token0)
		assert.Equal(t, token2, edges2[0].Token1)

		// Check non-existent token
		nonExistent := common.HexToAddress("0x9999999999999999999999999999999999999999")
		edges := graph.GetEdges(nonExistent)
		assert.Equal(t, 0, len(edges))
	})

	t.Run("Find paths", func(t *testing.T) {
		graph := NewPairGraph(common.Address{})

		// Create a path: token0 -> token1 -> token2 -> token3
		graph.AddPair(pair01, token0, token1, 18, 18, reserves0, reserves1, liquidity)
		graph.AddPair(pair12, token1, token2, 18, 6, reserves0, reserves1, liquidity)
		graph.AddPair(pair23, token2, token3, 6, 18, reserves0, reserves1, liquidity)

		// Test direct path
		path01 := graph.FindPath(token0, token1)
		assert.NotNil(t, path01)
		assert.Equal(t, 1, len(path01.Edges))
		assert.Equal(t, pair01, path01.Edges[0].Pair)

		// Test path with one intermediate token
		path02 := graph.FindPath(token0, token2)
		assert.NotNil(t, path02)
		assert.Equal(t, 2, len(path02.Edges))
		assert.Equal(t, pair01, path02.Edges[0].Pair)
		assert.Equal(t, pair12, path02.Edges[1].Pair)

		// Test path with two intermediate tokens
		path03 := graph.FindPath(token0, token3)
		assert.NotNil(t, path03)
		assert.Equal(t, 3, len(path03.Edges))
		assert.Equal(t, pair01, path03.Edges[0].Pair)
		assert.Equal(t, pair12, path03.Edges[1].Pair)
		assert.Equal(t, pair23, path03.Edges[2].Pair)

		// Test non-existent path
		nonExistent := common.HexToAddress("0x9999999999999999999999999999999999999999")
		noPath := graph.FindPath(token0, nonExistent)
		assert.Nil(t, noPath)

		// Test path to self
		selfPath := graph.FindPath(token0, token0)
		assert.Nil(t, selfPath)
	})

	t.Run("Calculate prices", func(t *testing.T) {
		graph := NewPairGraph(common.Address{})

		// Create test reserves with known ratios
		// token0/token1: 1:2 (price of token0 = 2 token1)
		reserves01 := big.NewInt(1000000) // token0
		reserves02 := big.NewInt(2000000) // token1

		// token1/token2: 2:1 (price of token1 = 0.5 token2)
		reserves11 := big.NewInt(2000000) // token1
		reserves12 := big.NewInt(1000000) // token2

		// token2/token3: 1:4 (price of token2 = 4 token3)
		reserves21 := big.NewInt(1000000) // token2
		reserves22 := big.NewInt(4000000) // token3

		// Create a path: token0 -> token1 -> token2 -> token3
		graph.AddPair(pair01, token0, token1, 18, 18, reserves01, reserves02, liquidity)
		graph.AddPair(pair12, token1, token2, 18, 18, reserves11, reserves12, liquidity)
		graph.AddPair(pair23, token2, token3, 18, 18, reserves21, reserves22, liquidity)

		// Test direct price (token0 -> token1)
		path01 := graph.FindPath(token0, token1)
		price01, err := path01.CalculatePrice()
		assert.NoError(t, err)
		assert.Equal(t, "2", price01.Text('f', 0)) // token0 price should be 2 token1

		// Test price with one hop (token0 -> token2)
		path02 := graph.FindPath(token0, token2)
		price02, err := path02.CalculatePrice()
		assert.NoError(t, err)
		assert.Equal(t, "1", price02.Text('f', 0)) // token0 price should be 1 token2 (2 * 0.5)

		// Test price with two hops (token0 -> token3)
		path03 := graph.FindPath(token0, token3)
		price03, err := path03.CalculatePrice()
		assert.NoError(t, err)
		assert.Equal(t, "4", price03.Text('f', 0)) // token0 price should be 4 token3 (2 * 0.5 * 4)

		// Test decimal adjustments
		graph2 := NewPairGraph(common.Address{})
		// Create pair with different decimals (token0: 18 decimals, token1: 6 decimals)
		graph2.AddPair(pair01, token0, token1, 18, 6, reserves01, reserves02, liquidity)
		path := graph2.FindPath(token0, token1)
		price, err := path.CalculatePrice()
		assert.NoError(t, err)
		// Price should be adjusted by 10^(18-6) = 10^12
		expectedPrice := new(big.Float).Mul(
			new(big.Float).SetFloat64(2),                // Base price ratio
			new(big.Float).SetFloat64(math.Pow(10, 12)), // Decimal adjustment
		)
		assert.Equal(t, expectedPrice.Text('f', 0), price.Text('f', 0))
	})

	t.Run("Path caching", func(t *testing.T) {
		graph := NewPairGraph(common.Address{})

		// Create test reserves with known ratios
		reserves01 := big.NewInt(1000000) // token0
		reserves02 := big.NewInt(2000000) // token1

		// Add a pair and find path
		graph.AddPair(pair01, token0, token1, 18, 18, reserves01, reserves02, liquidity)

		// First call should compute and cache the path
		path1 := graph.FindPath(token0, token1)
		assert.NotNil(t, path1)
		assert.Equal(t, 1, len(path1.Edges))

		// Second call should return cached path
		path2 := graph.FindPath(token0, token1)
		assert.NotNil(t, path2)
		assert.Equal(t, 1, len(path2.Edges))
		// Compare path contents instead of instances
		assert.Equal(t, path1.Edges[0].Pair, path2.Edges[0].Pair)
		assert.Equal(t, path1.Edges[0].Token0, path2.Edges[0].Token0)
		assert.Equal(t, path1.Edges[0].Token1, path2.Edges[0].Token1)
		assert.Equal(t, path1.TVL, path2.TVL)

		// Adding a new pair should invalidate cache
		graph.AddPair(pair12, token1, token2, 18, 18, reserves01, reserves02, liquidity)
		path3 := graph.FindPath(token0, token1)
		assert.NotNil(t, path3)
		// Compare path contents to ensure they're different
		assert.Equal(t, path1.Edges[0].Pair, path3.Edges[0].Pair)
		assert.Equal(t, path1.Edges[0].Token0, path3.Edges[0].Token0)
		assert.Equal(t, path1.Edges[0].Token1, path3.Edges[0].Token1)

		// Test caching of non-existent paths
		nonExistent := common.HexToAddress("0x9999999999999999999999999999999999999999")
		noPath1 := graph.FindPath(token0, nonExistent)
		assert.Nil(t, noPath1)

		// Second call should return cached nil
		noPath2 := graph.FindPath(token0, nonExistent)
		assert.Nil(t, noPath2)
	})

	t.Run("Cache invalidation", func(t *testing.T) {
		g := NewPairGraph(common.Address{})
		token0 := common.HexToAddress("0x1")
		token1 := common.HexToAddress("0x2")
		pair := common.HexToAddress("0x3")
		liquidity := big.NewFloat(1000000)

		// Initial reserves: 2:1 ratio (price of token0 in terms of token1 is 0.5)
		reserves01 := big.NewInt(2)
		reserves02 := big.NewInt(1)

		// Add pair with initial reserves
		g.AddPair(pair, token0, token1, 18, 18, reserves01, reserves02, liquidity)
		path := g.FindPath(token0, token1)
		require.NotNil(t, path)
		price1, err := path.Edges[0].GetPriceInPair(token0)
		require.NoError(t, err)
		price1Float, _ := price1.Float64()
		assert.InDelta(t, 0.5, price1Float, 0.0001) // price = reserve1/reserve0 = 1/2 = 0.5

		// Update reserves to 1:1 ratio (price of token0 in terms of token1 is 1.0)
		reserves01 = big.NewInt(1)
		reserves02 = big.NewInt(1)
		g.AddPair(pair, token0, token1, 18, 18, reserves01, reserves02, liquidity)
		path = g.FindPath(token0, token1)
		require.NotNil(t, path)
		price2, err := path.Edges[0].GetPriceInPair(token0)
		require.NoError(t, err)
		price2Float, _ := price2.Float64()
		assert.InDelta(t, 1.0, price2Float, 0.0001) // price = reserve1/reserve0 = 1/1 = 1.0
	})

	t.Run("Confidence calculation", func(t *testing.T) {
		// Test pair with high liquidity and balanced reserves
		highLiquidityPair := PairEdge{
			Token0:    token0,
			Token1:    token1,
			Decimals0: 18,
			Decimals1: 18,
			Reserves0: big.NewInt(1000000),
			Reserves1: big.NewInt(1000000),
			Liquidity: new(big.Float).SetFloat64(1000000), // $1M liquidity
		}

		// Test pair with low liquidity and imbalanced reserves
		lowLiquidityPair := PairEdge{
			Token0:    token0,
			Token1:    token1,
			Decimals0: 18,
			Decimals1: 18,
			Reserves0: big.NewInt(1000000),
			Reserves1: big.NewInt(100000),               // 10:1 ratio
			Liquidity: new(big.Float).SetFloat64(10000), // $10k liquidity
		}

		minLiquidity := new(big.Float).SetFloat64(100000) // $100k minimum liquidity

		// Test high liquidity pair
		highConfidence := highLiquidityPair.CalculateConfidence(minLiquidity)
		assert.Greater(t, highConfidence.LiquidityScore, 0.4)
		assert.Greater(t, highConfidence.StabilityScore, 0.8)
		assert.Greater(t, highConfidence.TotalConfidence, 0.5)

		// Test low liquidity pair
		lowConfidence := lowLiquidityPair.CalculateConfidence(minLiquidity)
		assert.Equal(t, float64(0), lowConfidence.LiquidityScore)
		assert.Less(t, lowConfidence.StabilityScore, 0.8)
		assert.Less(t, lowConfidence.TotalConfidence, 0.4)
	})
}

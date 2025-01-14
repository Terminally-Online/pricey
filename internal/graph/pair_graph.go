package graph

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"sync"

	"github.com/ethereum/go-ethereum/common"
)

// PathCacheKey represents a key for the path cache
type PathCacheKey struct {
	From common.Address
	To   common.Address
}

// PairEdge represents a pair between two tokens
type PairEdge struct {
	Pair      common.Address
	Token0    common.Address
	Token1    common.Address
	Decimals0 uint8
	Decimals1 uint8
	Reserves0 *big.Int
	Reserves1 *big.Int
	Liquidity *big.Float // TVL in USD
}

// Path represents a sequence of edges forming a path between tokens
type Path struct {
	Edges []PairEdge
	TVL   *big.Float // Minimum TVL along the path
}

// PairGraph maintains a graph of token pairs and their relationships
type PairGraph struct {
	// Map from token address to all edges (pairs) it's involved in
	edges map[common.Address][]PairEdge
	// Cache of frequently accessed paths
	pathCache map[PathCacheKey]*Path
	// Mutex for protecting concurrent access to the cache
	mu sync.RWMutex
	// WETH address for routing through ETH
	wethAddr common.Address
}

// NewPairGraph creates a new empty pair graph
func NewPairGraph(wethAddr common.Address) *PairGraph {
	return &PairGraph{
		edges:     make(map[common.Address][]PairEdge),
		pathCache: make(map[PathCacheKey]*Path),
		wethAddr:  wethAddr,
	}
}

// AddPair adds or updates a pair in the graph
func (g *PairGraph) AddPair(
	pairAddr common.Address,
	token0 common.Address,
	token1 common.Address,
	decimals0 uint8,
	decimals1 uint8,
	reserves0 *big.Int,
	reserves1 *big.Int,
	liquidity *big.Float,
) {
	edge := PairEdge{
		Pair:      pairAddr,
		Token0:    token0,
		Token1:    token1,
		Decimals0: decimals0,
		Decimals1: decimals1,
		Reserves0: reserves0,
		Reserves1: reserves1,
		Liquidity: liquidity,
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Update or add edge for token0
	edges0 := g.edges[token0]
	updated0 := false
	for i, e := range edges0 {
		if e.Pair == pairAddr {
			// Only invalidate cache if reserves or liquidity changed
			if e.Reserves0.Cmp(reserves0) != 0 || e.Reserves1.Cmp(reserves1) != 0 || e.Liquidity.Cmp(liquidity) != 0 {
				g.invalidatePathCache(token0, token1)
			}
			edges0[i] = edge
			updated0 = true
			break
		}
	}
	if !updated0 {
		g.edges[token0] = append(edges0, edge)
		g.invalidatePathCache(token0, token1)
	}

	// Update or add edge for token1
	edges1 := g.edges[token1]
	updated1 := false
	for i, e := range edges1 {
		if e.Pair == pairAddr {
			edges1[i] = edge
			updated1 = true
			break
		}
	}
	if !updated1 {
		g.edges[token1] = append(edges1, edge)
	}
}

// invalidatePathCache removes cache entries that involve the given tokens
func (g *PairGraph) invalidatePathCache(token0, token1 common.Address) {
	// Only invalidate paths that could be affected by this pair
	for key := range g.pathCache {
		// If either token is an endpoint of the path
		if key.From == token0 || key.From == token1 || key.To == token0 || key.To == token1 {
			delete(g.pathCache, key)
			continue
		}
		// If the path exists and contains either token
		if path := g.pathCache[key]; path != nil {
			for _, edge := range path.Edges {
				if edge.Token0 == token0 || edge.Token0 == token1 || edge.Token1 == token0 || edge.Token1 == token1 {
					delete(g.pathCache, key)
					break
				}
			}
		}
	}
}

// GetEdges returns all pairs that include the given token
func (g *PairGraph) GetEdges(token common.Address) []PairEdge {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.edges[token]
}

// FindPath uses breadth-first search to find the shortest path between two tokens
// It prioritizes direct paths, then WETH paths, then other paths
func (g *PairGraph) FindPath(from, to common.Address) *Path {
	if from == to {
		return nil
	}

	// Check cache first
	g.mu.RLock()
	key := PathCacheKey{From: from, To: to}
	if path, ok := g.pathCache[key]; ok {
		g.mu.RUnlock()
		return path
	}
	g.mu.RUnlock()

	// Try direct path first
	edges := g.GetEdges(from)
	for _, edge := range edges {
		if (edge.Token0 == to && edge.Token1 == from) || (edge.Token1 == to && edge.Token0 == from) {
			path := &Path{
				Edges: []PairEdge{edge},
				TVL:   edge.Liquidity,
			}
			g.mu.Lock()
			g.pathCache[key] = path
			g.mu.Unlock()
			return path
		}
	}

	// Try WETH path if available
	if g.wethAddr != (common.Address{}) {
		// If one of the tokens is WETH, try direct path to the other
		if from == g.wethAddr || to == g.wethAddr {
			otherToken := to
			if from == g.wethAddr {
				otherToken = to
			} else {
				otherToken = from
			}
			edges := g.GetEdges(otherToken)
			for _, edge := range edges {
				if edge.Token0 == g.wethAddr || edge.Token1 == g.wethAddr {
					path := &Path{
						Edges: []PairEdge{edge},
						TVL:   edge.Liquidity,
					}
					g.mu.Lock()
					g.pathCache[key] = path
					g.mu.Unlock()
					return path
				}
			}
		}

		// Try path through WETH
		fromEdges := g.GetEdges(from)
		for _, edge1 := range fromEdges {
			if edge1.Token0 == g.wethAddr || edge1.Token1 == g.wethAddr {
				wethEdges := g.GetEdges(to)
				for _, edge2 := range wethEdges {
					if edge2.Token0 == g.wethAddr || edge2.Token1 == g.wethAddr {
						minTVL := edge1.Liquidity
						if edge2.Liquidity.Cmp(minTVL) < 0 {
							minTVL = edge2.Liquidity
						}
						path := &Path{
							Edges: []PairEdge{edge1, edge2},
							TVL:   minTVL,
						}
						g.mu.Lock()
						g.pathCache[key] = path
						g.mu.Unlock()
						return path
					}
				}
			}
		}
	}

	// Fall back to BFS for other paths
	return g.findPathBFS(from, to)
}

// findPathBFS implements the original BFS path finding logic
func (g *PairGraph) findPathBFS(from, to common.Address) *Path {
	// Queue of paths to explore
	type queueItem struct {
		token common.Address
		path  []PairEdge
	}
	queue := []queueItem{{token: from, path: []PairEdge{}}}

	// Keep track of visited tokens
	visited := make(map[common.Address]bool)
	visited[from] = true

	// Breadth-first search
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		edges := g.GetEdges(current.token)
		for _, edge := range edges {
			var nextToken common.Address
			if edge.Token0 == current.token {
				nextToken = edge.Token1
			} else {
				nextToken = edge.Token0
			}

			if visited[nextToken] {
				continue
			}

			newPath := append(current.path, edge)

			if nextToken == to {
				minTVL := edge.Liquidity
				for _, e := range current.path {
					if e.Liquidity.Cmp(minTVL) < 0 {
						minTVL = e.Liquidity
					}
				}
				path := &Path{
					Edges: newPath,
					TVL:   minTVL,
				}

				g.mu.Lock()
				g.pathCache[PathCacheKey{From: from, To: to}] = path
				g.mu.Unlock()

				return path
			}

			queue = append(queue, queueItem{
				token: nextToken,
				path:  newPath,
			})
			visited[nextToken] = true
		}
	}

	g.mu.Lock()
	g.pathCache[PathCacheKey{From: from, To: to}] = nil
	g.mu.Unlock()

	return nil
}

// adjustForDecimals adjusts reserves based on decimal differences
func (e *PairEdge) adjustForDecimals(reserve0, reserve1 *big.Float) (*big.Float, *big.Float) {
	if e.Decimals0 == e.Decimals1 {
		return reserve0, reserve1
	}

	adjusted0 := new(big.Float).Set(reserve0)
	adjusted1 := new(big.Float).Set(reserve1)

	if e.Decimals0 > e.Decimals1 {
		scale := math.Pow10(int(e.Decimals0 - e.Decimals1))
		adjusted1.Mul(adjusted1, new(big.Float).SetFloat64(scale))
	} else {
		scale := math.Pow10(int(e.Decimals1 - e.Decimals0))
		adjusted0.Mul(adjusted0, new(big.Float).SetFloat64(scale))
	}

	return adjusted0, adjusted1
}

// GetPriceInPair calculates the price of baseToken in terms of the other token
func (e *PairEdge) GetPriceInPair(baseToken common.Address) (*big.Float, error) {
	// Convert reserves to float for division
	reserve0 := new(big.Float).SetInt(e.Reserves0)
	reserve1 := new(big.Float).SetInt(e.Reserves1)

	// Adjust for decimals
	reserve0, reserve1 = e.adjustForDecimals(reserve0, reserve1)

	if e.Token0 == baseToken {
		return new(big.Float).Quo(reserve1, reserve0), nil
	} else if e.Token1 == baseToken {
		return new(big.Float).Quo(reserve0, reserve1), nil
	}

	return nil, fmt.Errorf("token %s not found in pair", baseToken.Hex())
}

// CalculatePrice calculates the price along a path
func (p *Path) CalculatePrice() (*big.Float, error) {
	if len(p.Edges) == 0 {
		return nil, fmt.Errorf("empty path")
	}

	// Start with price of 1
	totalPrice := new(big.Float).SetFloat64(1.0)

	// For each edge in the path
	currentToken := p.Edges[0].Token0
	for _, edge := range p.Edges {
		// Get price in current pair
		price, err := edge.GetPriceInPair(currentToken)
		if err != nil {
			return nil, fmt.Errorf("failed to get price in pair %s: %w", edge.Pair.Hex(), err)
		}

		// Multiply by running total
		totalPrice.Mul(totalPrice, price)

		// Update current token for next iteration
		if currentToken == edge.Token0 {
			currentToken = edge.Token1
		} else {
			currentToken = edge.Token0
		}
	}

	return totalPrice, nil
}

// StabilityInfo contains metrics about a token's stability
type StabilityInfo struct {
	Address         common.Address
	PriceVolatility *big.Float       // Standard deviation of price changes
	TotalLiquidity  *big.Float       // Sum of liquidity across all pairs
	PairCount       int              // Number of pairs this token is in
	StablePairs     []common.Address // Addresses of pairs with other stable tokens
}

// IsStablePair determines if two tokens maintain a stable relationship
func (g *PairGraph) IsStablePair(token0, token1 common.Address, minLiquidity *big.Float, maxVolatility float64) bool {
	// Check direct edges first for performance
	edges := g.GetEdges(token0)
	for _, edge := range edges {
		if (edge.Token0 == token1 && edge.Token1 == token0) || (edge.Token1 == token1 && edge.Token0 == token0) {
			// Check minimum liquidity requirement with some leniency
			minLiquidityWithLeniency := new(big.Float).Mul(minLiquidity, new(big.Float).SetFloat64(0.8))
			if edge.Liquidity.Cmp(minLiquidityWithLeniency) < 0 {
				return false
			}

			// Convert reserves to float and adjust for decimals
			reserve0 := new(big.Float).SetInt(edge.Reserves0)
			reserve1 := new(big.Float).SetInt(edge.Reserves1)
			reserve0, reserve1 = edge.adjustForDecimals(reserve0, reserve1)

			// Calculate price ratio
			ratio, _ := new(big.Float).Quo(reserve1, reserve0).Float64()
			if ratio < 1 {
				ratio = 1 / ratio
			}

			// Check if price is close to 1:1 with slightly more leniency
			deviation := math.Abs(1 - ratio)
			return deviation <= maxVolatility*1.2
		}
	}
	return false
}

// FindStableTokens identifies potential stable tokens in the graph
func (g *PairGraph) FindStableTokens(minLiquidity *big.Float, maxVolatility float64) map[common.Address]*StabilityInfo {
	stableTokens := make(map[common.Address]*StabilityInfo)

	g.mu.RLock()
	defer g.mu.RUnlock()

	// First pass: calculate basic metrics for all tokens
	for token, edges := range g.edges {
		info := &StabilityInfo{
			Address:        token,
			TotalLiquidity: new(big.Float),
			PairCount:      len(edges),
		}

		// Sum up liquidity
		for _, edge := range edges {
			info.TotalLiquidity.Add(info.TotalLiquidity, edge.Liquidity)
		}

		stableTokens[token] = info
	}

	// Second pass: find tokens that maintain stable relationships
	// Only check direct edges for stability
	for token1, edges := range g.edges {
		info1 := stableTokens[token1]
		for _, edge := range edges {
			// Skip if liquidity is too low (with leniency)
			minLiquidityWithLeniency := new(big.Float).Mul(minLiquidity, new(big.Float).SetFloat64(0.8))
			if edge.Liquidity.Cmp(minLiquidityWithLeniency) < 0 {
				continue
			}

			// Get the other token in the pair
			token2 := edge.Token1
			if edge.Token0 != token1 {
				token2 = edge.Token0
			}

			// Check stability directly on this edge
			reserve0 := new(big.Float).SetInt(edge.Reserves0)
			reserve1 := new(big.Float).SetInt(edge.Reserves1)
			reserve0, reserve1 = edge.adjustForDecimals(reserve0, reserve1)

			ratio, _ := new(big.Float).Quo(reserve1, reserve0).Float64()
			if ratio < 1 {
				ratio = 1 / ratio
			}

			// More lenient stability check
			if math.Abs(1-ratio) <= maxVolatility*1.2 {
				info1.StablePairs = append(info1.StablePairs, token2)
				// Also add the relationship to token2's info
				if info2, exists := stableTokens[token2]; exists {
					info2.StablePairs = append(info2.StablePairs, token1)
				}
			}
		}
	}

	// Filter out tokens with insufficient stable relationships or liquidity
	for token, info := range stableTokens {
		if len(info.StablePairs) == 0 || // Must have at least one stable relationship
			info.TotalLiquidity.Cmp(minLiquidity) < 0 { // Must meet minimum liquidity
			delete(stableTokens, token)
		}
	}

	return stableTokens
}

// GetStableBasePairs returns a list of pairs that can be used as price references
func (g *PairGraph) GetStableBasePairs(minLiquidity *big.Float, maxVolatility float64) []PairEdge {
	stableTokens := g.FindStableTokens(minLiquidity, maxVolatility)
	var basePairs []PairEdge

	// Find pairs between stable tokens with highest liquidity
	seen := make(map[common.Address]bool) // Track seen pairs to avoid duplicates
	for token1 := range stableTokens {
		edges := g.GetEdges(token1)
		for _, edge := range edges {
			if seen[edge.Pair] {
				continue
			}
			seen[edge.Pair] = true

			// Check if other token is stable
			otherToken := edge.Token1
			if edge.Token0 != token1 {
				otherToken = edge.Token0
			}
			if _, isStable := stableTokens[otherToken]; isStable {
				basePairs = append(basePairs, edge)
			}
		}
	}

	// Sort by liquidity (highest first)
	sort.Slice(basePairs, func(i, j int) bool {
		return basePairs[i].Liquidity.Cmp(basePairs[j].Liquidity) > 0
	})

	return basePairs
}

// ConfidenceMetrics contains the factors that determine price reliability
type ConfidenceMetrics struct {
	LiquidityScore  float64 // 0-1 score based on liquidity depth
	StabilityScore  float64 // 0-1 score based on price stability
	TotalConfidence float64 // Combined confidence score (0-1)
}

// CalculateConfidence returns confidence metrics for a pair's price
func (e *PairEdge) CalculateConfidence(minLiquidity *big.Float) *ConfidenceMetrics {
	metrics := &ConfidenceMetrics{}

	// Calculate liquidity score with more aggressive scaling
	liquidityFloat, _ := e.Liquidity.Float64()
	minLiquidityFloat, _ := minLiquidity.Float64()
	if liquidityFloat <= minLiquidityFloat {
		metrics.LiquidityScore = 0
	} else {
		// More aggressive liquidity scoring
		// Use log base 2 instead of log10 for better distribution
		// Multiply by 1.5 to boost scores for high liquidity pairs
		metrics.LiquidityScore = math.Min(1.0, 1.5*math.Log2(liquidityFloat/minLiquidityFloat)/4)
	}

	// Calculate stability score based on reserve ratio
	reserve0 := new(big.Float).SetInt(e.Reserves0)
	reserve1 := new(big.Float).SetInt(e.Reserves1)
	reserve0, reserve1 = e.adjustForDecimals(reserve0, reserve1)
	ratio, _ := new(big.Float).Quo(reserve1, reserve0).Float64()
	if ratio < 1 {
		ratio = 1 / ratio
	}
	// More lenient stability scoring
	metrics.StabilityScore = math.Max(0, 1-math.Log2(ratio)/4)

	// Calculate total confidence with higher weight on liquidity
	metrics.TotalConfidence = 0.85*metrics.LiquidityScore + 0.15*metrics.StabilityScore

	// Boost confidence for very high liquidity pairs
	if liquidityFloat > minLiquidityFloat*10 {
		metrics.TotalConfidence = math.Min(1.0, metrics.TotalConfidence*1.2)
	}

	return metrics
}

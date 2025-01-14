package price

import (
	"context"
	"fmt"
	"math/big"
	"sort"

	"pricey/internal/graph"
	"pricey/internal/service/uniswap"
	"pricey/pkg/types"

	"github.com/ethereum/go-ethereum/common"
)

type Calculator struct {
	uniswap *uniswap.Service
	graph   *graph.PairGraph
}

func NewCalculator(uniswap *uniswap.Service) *Calculator {
	// Initialize with WETH address for routing optimization
	wethAddr := common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2")
	return &Calculator{
		uniswap: uniswap,
		graph:   graph.NewPairGraph(wethAddr),
	}
}

// GetUSDPrice returns the USD price for a token
func (c *Calculator) GetUSDPrice(ctx context.Context, tokenAddress string, opts *types.PriceOpts) (*types.Price, error) {
	token := common.HexToAddress(tokenAddress)

	// Find stable base pairs to use as price reference
	minLiquidity := new(big.Float).SetFloat64(500000) // $500k minimum liquidity
	maxVolatility := 0.02                             // 2% maximum deviation
	stablePairs := c.graph.GetStableBasePairs(minLiquidity, maxVolatility)
	if len(stablePairs) == 0 {
		return nil, fmt.Errorf("no stable pairs found for price reference")
	}

	// Sort stable pairs by their confidence score
	type pairScore struct {
		pair       graph.PairEdge
		confidence float64
	}
	scoredPairs := make([]pairScore, 0, len(stablePairs))
	for _, pair := range stablePairs {
		metrics := pair.CalculateConfidence(minLiquidity)
		scoredPairs = append(scoredPairs, pairScore{pair, metrics.TotalConfidence})
	}
	sort.Slice(scoredPairs, func(i, j int) bool {
		return scoredPairs[i].confidence > scoredPairs[j].confidence
	})

	// Try to find paths through the top 3 most confident stable pairs
	maxPaths := 3
	if len(scoredPairs) > maxPaths {
		scoredPairs = scoredPairs[:maxPaths]
	}

	var paths []*graph.Path
	var pathConfidences []float64

	for _, sp := range scoredPairs {
		// Try path to both tokens in the stable pair
		for _, stableToken := range []common.Address{sp.pair.Token0, sp.pair.Token1} {
			path := c.graph.FindPath(token, stableToken)
			if path == nil {
				continue
			}

			// Calculate confidence for this path
			confidence := c.calculatePathConfidence(path, minLiquidity)

			// Only consider paths with reasonable confidence
			if confidence > 0.3 { // 30% minimum confidence threshold
				paths = append(paths, path)
				pathConfidences = append(pathConfidences, confidence)
			}
		}
	}

	if len(paths) == 0 {
		return nil, fmt.Errorf("no valid price path found for token %s", tokenAddress)
	}

	// Calculate weighted average price from all valid paths
	totalWeight := 0.0
	weightedPrice := new(big.Float)
	var bestTVL *big.Float

	for i, path := range paths {
		price, err := path.CalculatePrice()
		if err != nil {
			continue
		}

		weight := pathConfidences[i]
		totalWeight += weight

		// Add weighted price to total
		weightedContribution := new(big.Float).Mul(price, new(big.Float).SetFloat64(weight))
		weightedPrice.Add(weightedPrice, weightedContribution)

		// Track highest TVL
		if bestTVL == nil || path.TVL.Cmp(bestTVL) > 0 {
			bestTVL = path.TVL
		}
	}

	if totalWeight == 0 {
		return nil, fmt.Errorf("failed to calculate valid price for token %s", tokenAddress)
	}

	// Normalize the weighted price
	weightedPrice.Quo(weightedPrice, new(big.Float).SetFloat64(totalWeight))

	// Use the path with highest confidence for additional data
	bestPathIndex := 0
	for i, conf := range pathConfidences {
		if conf > pathConfidences[bestPathIndex] {
			bestPathIndex = i
		}
	}

	return &types.Price{
		Path:       pathToStrings(paths[bestPathIndex]),
		PriceUSD:   weightedPrice,
		Reserves0:  paths[bestPathIndex].Edges[0].Reserves0,
		Reserves1:  paths[bestPathIndex].Edges[0].Reserves1,
		TVL:        bestTVL,
		Confidence: totalWeight / float64(len(paths)), // Average confidence across all used paths
	}, nil
}

// calculatePathConfidence returns a confidence score for the entire path
func (c *Calculator) calculatePathConfidence(path *graph.Path, minLiquidity *big.Float) float64 {
	if len(path.Edges) == 0 {
		return 0
	}

	// Calculate confidence for each edge
	edgeConfidences := make([]float64, len(path.Edges))
	minEdgeConfidence := 1.0

	for i, edge := range path.Edges {
		metrics := edge.CalculateConfidence(minLiquidity)
		edgeConfidences[i] = metrics.TotalConfidence
		if metrics.TotalConfidence < minEdgeConfidence {
			minEdgeConfidence = metrics.TotalConfidence
		}
	}

	// Path length penalties
	pathPenalty := float64(len(path.Edges)-1) * 0.15 // 15% penalty per additional hop
	if pathPenalty > 0.6 {                           // Cap maximum penalty at 60%
		pathPenalty = 0.6
	}

	// Final confidence is the minimum edge confidence with path length penalty
	// This ensures that a path is only as strong as its weakest link
	return minEdgeConfidence * (1 - pathPenalty)
}

// pathToStrings converts a path to a slice of address strings
func pathToStrings(path *graph.Path) []string {
	if len(path.Edges) == 0 {
		return nil
	}

	result := make([]string, 0, len(path.Edges)+1)
	currentToken := path.Edges[0].Token0

	for _, edge := range path.Edges {
		result = append(result, currentToken.Hex())
		if currentToken == edge.Token0 {
			currentToken = edge.Token1
		} else {
			currentToken = edge.Token0
		}
	}
	result = append(result, currentToken.Hex())

	return result
}

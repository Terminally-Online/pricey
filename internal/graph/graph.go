package graph

import (
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

type Edge struct {
	Token0     common.Address
	Token1     common.Address
	PairAddr   common.Address
	Reserves0  *big.Int
	Reserves1  *big.Int
	Liquidity  *big.Float
	LastUpdate time.Time
	Decimals0  uint8
	Decimals1  uint8
}

type Node struct {
	Address  common.Address
	Decimals uint8
	Edges    map[common.Address]*Edge
}

type PriceGraph struct {
	mu    sync.RWMutex
	nodes map[common.Address]*Node

	wethAddr common.Address
	stables  map[common.Address]struct{}
}

func NewPriceGraph(wethAddr common.Address, stableAddrs ...common.Address) *PriceGraph {
	stables := make(map[common.Address]struct{})
	for _, addr := range stableAddrs {
		stables[addr] = struct{}{}
	}

	return &PriceGraph{
		nodes:    make(map[common.Address]*Node),
		wethAddr: wethAddr,
		stables:  stables,
	}
}

func (g *PriceGraph) AddPair(
	pairAddr common.Address,
	token0 common.Address,
	token1 common.Address,
	decimals0 uint8,
	decimals1 uint8,
	reserves0 *big.Int,
	reserves1 *big.Int,
	liquidity *big.Float,
) {
	g.mu.Lock()
	defer g.mu.Unlock()

	node0 := g.getOrCreateNode(token0, decimals0)
	node1 := g.getOrCreateNode(token1, decimals1)

	edge := &Edge{
		Token0:     token0,
		Token1:     token1,
		PairAddr:   pairAddr,
		Reserves0:  new(big.Int).Set(reserves0),
		Reserves1:  new(big.Int).Set(reserves1),
		Liquidity:  liquidity,
		LastUpdate: time.Now(),
		Decimals0:  decimals0,
		Decimals1:  decimals1,
	}

	node0.Edges[token1] = edge
	node1.Edges[token0] = edge
}

func (g *PriceGraph) getOrCreateNode(addr common.Address, decimals uint8) *Node {
	if node, exists := g.nodes[addr]; exists {
		return node
	}

	node := &Node{
		Address:  addr,
		Decimals: decimals,
		Edges:    make(map[common.Address]*Edge),
	}
	g.nodes[addr] = node
	return node
}

func (g *PriceGraph) FindBestPath(from, to common.Address) ([]*Edge, float64) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	fromNode, fromExists := g.nodes[from]
	_, toExists := g.nodes[to]
	if !fromExists || !toExists {
		return nil, 0
	}

	if edge, exists := fromNode.Edges[to]; exists {
		return []*Edge{edge}, 1.0
	}

	wethPath := g.findPathThroughWETH(from, to)
	if len(wethPath) > 0 {
		return wethPath, 0.9
	}

	for stable := range g.stables {
		if path := g.findPathThroughToken(from, to, stable); len(path) > 0 {
			return path, 0.8
		}
	}

	return nil, 0
}

func (g *PriceGraph) findPathThroughWETH(from, to common.Address) []*Edge {
	if from == g.wethAddr {
		if toNode, exists := g.nodes[to]; exists {
			if edge, exists := toNode.Edges[g.wethAddr]; exists {
				return []*Edge{edge}
			}
		}
		return nil
	}
	if to == g.wethAddr {
		if fromNode, exists := g.nodes[from]; exists {
			if edge, exists := fromNode.Edges[g.wethAddr]; exists {
				return []*Edge{edge}
			}
		}
		return nil
	}

	var path []*Edge

	if fromNode, exists := g.nodes[from]; exists {
		if edge1, exists := fromNode.Edges[g.wethAddr]; exists {
			path = append(path, edge1)
		} else {
			return nil
		}
	}

	if wethNode, exists := g.nodes[g.wethAddr]; exists {
		if edge2, exists := wethNode.Edges[to]; exists {
			path = append(path, edge2)
		} else {
			return nil
		}
	}

	return path
}

func (g *PriceGraph) findPathThroughToken(from, to, through common.Address) []*Edge {
	var path []*Edge

	if fromNode, exists := g.nodes[from]; exists {
		if edge1, exists := fromNode.Edges[through]; exists {
			path = append(path, edge1)
		} else {
			return nil
		}
	}

	if throughNode, exists := g.nodes[through]; exists {
		if edge2, exists := throughNode.Edges[to]; exists {
			path = append(path, edge2)
		} else {
			return nil
		}
	}

	return path
}

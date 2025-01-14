package types

import (
	"math/big"
	"time"
)

// Price represents a token price at a specific time
type Price struct {
	Token0Address string
	Token1Address string
	Price0        *big.Float
	Price1        *big.Float
	PriceUSD      *big.Float // Price in USD
	Timestamp     time.Time
	BlockNumber   uint64
	Reserves0     *big.Int
	Reserves1     *big.Int
	// Metadata about the price calculation
	Path       []string // Addresses of tokens used to calculate USD price
	Confidence float64  // 0-1 score of price confidence based on liquidity and path
}

// PriceOpts contains options for price calculation
type PriceOpts struct {
	BlockNumber *big.Int // optional: specific block number for historical prices
}

func (o *PriceOpts) GetBlockNumber() *big.Int {
	if o == nil {
		return nil
	}
	return o.BlockNumber
}

package price

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/big"

	"pricey/internal/service/uniswap"
	"pricey/pkg/types"

	"github.com/ethereum/go-ethereum/common"
)

const (
	// Common stablecoin addresses on Ethereum mainnet
	USDCAddress = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
	USDTAddress = "0xdAC17F958D2ee523a2206206994597C13D831ec7"
	DAIAddress  = "0x6B175474E89094C44Da98b954EedeAC495271d0F"
	WETHAddress = "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"
)

type Calculator struct {
	uniswap       *uniswap.Service
	tokenDecimals map[string]int
	stablePairs   map[string]common.Address
}

func NewCalculator(uniswap *uniswap.Service) *Calculator {
	return &Calculator{
		uniswap: uniswap,
		tokenDecimals: map[string]int{
			USDCAddress: 6,
			USDTAddress: 6,
			DAIAddress:  18,
			WETHAddress: 18,
		},
		stablePairs: map[string]common.Address{
			WETHAddress: common.HexToAddress("0xB4e16d0168e52d35CaCD2c6185b44281Ec28C9Dc"), // WETH/USDC
			USDCAddress: common.HexToAddress("0x3041CbD36888bECc7bbCBc0045E3B1f144466f5f"), // USDC/USDT
		},
	}
}

// GetUSDPrice returns the USD price for a token
func (c *Calculator) GetUSDPrice(ctx context.Context, tokenAddress string, opts *types.PriceOpts) (*types.Price, error) {
	log.Printf("Calculating price for %s", tokenAddress)

	// Try USDC first
	if pairAddr, ok := c.stablePairs[tokenAddress]; ok {
		price, err := c.uniswap.GetPrice(ctx, pairAddr, opts)
		if err == nil {
			// For WETH/USDC pair, WETH is token0
			if tokenAddress == WETHAddress {
				price.Path = []string{WETHAddress, USDCAddress}
			} else {
				// For USDC/USDT pair, USDC is token0
				price.Path = []string{USDCAddress, USDTAddress}
			}
			return c.adjustPriceForDecimals(price, USDCAddress)
		}
	}

	return nil, fmt.Errorf("no direct USD pair found for token %s", tokenAddress)
}

func (c *Calculator) adjustPriceForDecimals(price *types.Price, stablecoin string) (*types.Price, error) {
	token0 := price.Path[0]
	token1 := price.Path[1]

	token0Decimals := c.tokenDecimals[token0]
	token1Decimals := c.tokenDecimals[token1]

	log.Printf("Token0: %s (decimals: %d)", token0, token0Decimals)
	log.Printf("Token1: %s (decimals: %d)", token1, token1Decimals)
	log.Printf("Reserves0: %s", price.Reserves0.String())
	log.Printf("Reserves1: %s", price.Reserves1.String())

	// Convert reserves to big.Float for decimal arithmetic
	reserves0 := new(big.Float).SetInt(price.Reserves0)
	reserves1 := new(big.Float).SetInt(price.Reserves1)

	// Calculate price based on which token is the stablecoin
	var priceUSD *big.Float
	if token0 == stablecoin {
		// If stablecoin is token0, price = reserves0 / reserves1 * 10^(token1Decimals - token0Decimals)
		priceUSD = new(big.Float).Quo(reserves0, reserves1)
		decimalAdjustment := math.Pow10(token1Decimals - token0Decimals)
		priceUSD.Mul(priceUSD, new(big.Float).SetFloat64(decimalAdjustment))
	} else {
		// If stablecoin is token1, price = reserves1 / reserves0 * 10^(token1Decimals - token0Decimals)
		priceUSD = new(big.Float).Quo(reserves1, reserves0)
		decimalAdjustment := math.Pow10(token1Decimals - token0Decimals)
		priceUSD.Mul(priceUSD, new(big.Float).SetFloat64(decimalAdjustment))
	}

	// If we're getting the price of a non-stablecoin in terms of a stablecoin,
	// we need to invert the price
	if token0 != stablecoin && token1 == stablecoin {
		priceUSD = new(big.Float).Quo(new(big.Float).SetFloat64(1), priceUSD)
	}

	log.Printf("Calculated price: %f", priceUSD)
	price.PriceUSD = priceUSD
	price.Confidence = calculateConfidence(price)
	return price, nil
}

// calculateConfidence returns a 0-1 score based on liquidity and path length
func calculateConfidence(price *types.Price) float64 {
	// For direct pairs, return perfect confidence
	if len(price.Path) == 2 {
		return 1.0
	}

	// Start with perfect confidence
	confidence := 1.0

	// Reduce confidence based on path length
	pathPenalty := float64(len(price.Path)-2) * 0.1
	confidence -= pathPenalty

	// TODO: Add liquidity-based confidence calculation
	// This would look at the reserves and compare them to
	// typical liquidity levels for this type of pair

	if confidence < 0 {
		confidence = 0
	}
	return confidence
}

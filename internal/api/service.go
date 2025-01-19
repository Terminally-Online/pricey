package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gorilla/mux"

	"pricey/internal/database"
	"pricey/internal/service/uniswap"
	"pricey/pkg/types"
)

type Service struct {
	server *http.Server
	price  *uniswap.Service
	db     *database.DB
}

type PriceResponse struct {
	Token      string  `json:"token"`
	PriceUSD   float64 `json:"price"`
	Timestamp  int64   `json:"timestamp"`
	Confidence float64 `json:"confidence"`
}

type TokenListResponse struct {
	Tokens []TokenInfo `json:"tokens"`
}

type TokenInfo struct {
	Address    string    `json:"address"`
	Name       string    `json:"name"`
	Symbol     string    `json:"symbol"`
	Decimals   int       `json:"decimals"`
	PriceUSD   *float64  `json:"price"`
	Confidence *float64  `json:"confidence"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func NewService(priceService *uniswap.Service, db *database.DB, port int) *Service {
	s := &Service{
		price: priceService,
		db:    db,
	}

	r := mux.NewRouter()
	r.HandleFunc("/v1/price/{token}", s.handleGetPrice).Methods("GET")
	r.HandleFunc("/v1/tokens", s.handleListTokens).Methods("GET")

	s.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	return s
}

func (s *Service) Start() error {
	return s.server.ListenAndServe()
}

func (s *Service) Stop(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func (s *Service) handleGetPrice(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	tokenAddr := vars["token"]

	if !common.IsHexAddress(tokenAddr) {
		http.Error(w, "Invalid token address", http.StatusBadRequest)
		return
	}

	token := common.HexToAddress(tokenAddr)
	price, err := s.price.GetPrice(r.Context(), token, &types.PriceOpts{})
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get price: %v", err), http.StatusInternalServerError)
		return
	}

	priceUSD, _ := price.PriceUSD.Float64()
	response := PriceResponse{
		Token:      tokenAddr,
		PriceUSD:   priceUSD,
		Confidence: price.Confidence,
		Timestamp:  price.Timestamp.Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Service) handleListTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.db.GetActiveTokens(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get tokens: %v", err), http.StatusInternalServerError)
		return
	}

	response := TokenListResponse{
		Tokens: make([]TokenInfo, len(tokens)),
	}

	for i, t := range tokens {
		var priceUSD, confidence *float64
		if t.PriceUSD.Valid {
			val := t.PriceUSD.Float64
			priceUSD = &val
		}
		if t.Confidence.Valid {
			val := t.Confidence.Float64
			confidence = &val
		}

		response.Tokens[i] = TokenInfo{
			Address:    "0x" + t.Address,
			Name:       t.Name,
			Symbol:     t.Symbol,
			Decimals:   t.Decimals,
			PriceUSD:   priceUSD,
			Confidence: confidence,
			UpdatedAt:  t.UpdatedAt,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

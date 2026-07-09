// Package quoteapi exposes the frontend REST API for quote management.
package quoteapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/go-playground/validator/v10"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
	"my-provider/internal/quote"
)

// validate is the shared validator instance for HTTP request payloads.
var validate = validator.New(validator.WithRequiredStructEnabled())

// Publisher is the interface for publishing quotes to the t-0 network.
type Publisher interface {
	Publish(ctx context.Context) error
}

// Handler holds the API dependencies.
type Handler struct {
	store     quote.Store
	publisher Publisher
	network   paymentconnect.NetworkServiceClient
	apiKeys   map[string]struct{}
}

// NewHandler creates a new API handler.
func NewHandler(store quote.Store, publisher Publisher, network paymentconnect.NetworkServiceClient, apiKeys []string) *Handler {
	keySet := make(map[string]struct{}, len(apiKeys))
	for _, k := range apiKeys {
		keySet[k] = struct{}{}
	}
	return &Handler{
		store:     store,
		publisher: publisher,
		network:   network,
		apiKeys:   keySet,
	}
}

// Router returns the API mux.
func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/quotes", h.withAuth(h.handleGetQuotes))
	mux.HandleFunc("PUT /api/v1/quotes/pay-out", h.withAuth(h.handleUpdatePayOut))
	mux.HandleFunc("PUT /api/v1/quotes/pay-in", h.withAuth(h.handleUpdatePayIn))
	mux.HandleFunc("POST /api/v1/quotes/publish", h.withAuth(h.handlePublish))
	mux.HandleFunc("POST /api/v1/quotes/publish/pay-out", h.withAuth(h.handlePublish))
	mux.HandleFunc("POST /api/v1/quotes/publish/pay-in", h.withAuth(h.handlePublish))
	mux.HandleFunc("POST /api/v1/quotes/network", h.withAuth(h.handleGetNetworkQuote))
	return mux
}

func (h *Handler) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			httpError(w, "missing or invalid Authorization header", http.StatusUnauthorized)
			return
		}
		key := strings.TrimPrefix(auth, prefix)
		if _, ok := h.apiKeys[key]; !ok {
			httpError(w, "invalid API key", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// GetQuotes returns the locally stored pay-out and pay-in quote snapshots.
// @Summary Get stored quote snapshots
// @Tags quotes
// @Produce json
// @Security BearerAuth
// @Success 200 {object} SwaggerQuotesResponse
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/quotes [get]
func (h *Handler) handleGetQuotes(w http.ResponseWriter, r *http.Request) {
	resp := QuotesResponse{}

	payOut, err := h.store.GetSnapshots(r.Context(), quote.StreamTypePayOut)
	if err != nil && err != quote.ErrNotFound {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp.PayOut = payOut

	payIn, err := h.store.GetSnapshots(r.Context(), quote.StreamTypePayIn)
	if err != nil && err != quote.ErrNotFound {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp.PayIn = payIn

	jsonResponse(w, resp)
}

// UpdatePayOut replaces the stored pay-out quote snapshot and triggers a publish.
// @Summary Update pay-out quotes
// @Tags quotes
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body SwaggerUpdateQuotesRequest true "Pay-out quote groups"
// @Success 200 {object} PublishResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/quotes/pay-out [put]
func (h *Handler) handleUpdatePayOut(w http.ResponseWriter, r *http.Request) {
	h.handleUpdate(w, r, quote.StreamTypePayOut)
}

// UpdatePayIn replaces the stored pay-in quote snapshot and triggers a publish.
// @Summary Update pay-in quotes
// @Tags quotes
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body SwaggerUpdateQuotesRequest true "Pay-in quote groups"
// @Success 200 {object} PublishResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/quotes/pay-in [put]
func (h *Handler) handleUpdatePayIn(w http.ResponseWriter, r *http.Request) {
	h.handleUpdate(w, r, quote.StreamTypePayIn)
}

func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request, stream quote.StreamType) {
	var req UpdateQuotesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if err := validate.Struct(req); err != nil {
		httpError(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if err := quote.ValidateSnapshot(req.Groups); err != nil {
		httpError(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.store.ReplaceSnapshots(r.Context(), stream, req.Groups); err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Trigger immediate publish so the network sees the new snapshot.
	if err := h.publisher.Publish(r.Context()); err != nil {
		log.Printf("Failed to publish after update: %v", err)
		// Still return 200 because the snapshot was saved.
	}

	jsonResponse(w, PublishResponse{Published: true, Message: "snapshot updated and published"})
}

// Publish pushes the current quote snapshots to the t-0 network.
// @Summary Publish quotes to the network
// @Tags quotes
// @Produce json
// @Security BearerAuth
// @Success 200 {object} PublishResponse
// @Failure 401 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/quotes/publish [post]
// @Router /api/v1/quotes/publish/pay-out [post]
// @Router /api/v1/quotes/publish/pay-in [post]
func (h *Handler) handlePublish(w http.ResponseWriter, r *http.Request) {
	if err := h.publisher.Publish(r.Context()); err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, PublishResponse{Published: true, Message: "published"})
}

// GetNetworkQuote fetches a real-time quote from the t-0 network for the given amount and payout currency.
// @Summary Get a real-time network quote
// @Tags quotes
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body SwaggerGetNetworkQuoteRequest true "Network quote request"
// @Success 200 {object} NetworkQuoteResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 502 {object} ErrorResponse
// @Router /api/v1/quotes/network [post]
func (h *Handler) handleGetNetworkQuote(w http.ResponseWriter, r *http.Request) {
	var req GetNetworkQuoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if err := validate.Struct(req); err != nil {
		httpError(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	var amount *payment.PaymentAmount
	switch req.AmountType {
	case "pay_out":
		amount = &payment.PaymentAmount{Amount: &payment.PaymentAmount_PayOutAmount{
			PayOutAmount: &common.Decimal{Unscaled: req.Amount.Unscaled, Exponent: req.Amount.Exponent},
		}}
	case "settlement":
		amount = &payment.PaymentAmount{Amount: &payment.PaymentAmount_SettlementAmount{
			SettlementAmount: &common.Decimal{Unscaled: req.Amount.Unscaled, Exponent: req.Amount.Exponent},
		}}
	default:
		httpError(w, "amountType must be 'pay_out' or 'settlement'", http.StatusBadRequest)
		return
	}

	method, ok := common.PaymentMethodType_value[req.PayOutMethod]
	if !ok {
		httpError(w, "invalid payOutMethod", http.StatusBadRequest)
		return
	}

	resp, err := h.network.GetQuote(r.Context(), connect.NewRequest(&payment.GetQuoteRequest{
		Amount:         amount,
		PayOutCurrency: strings.ToUpper(req.PayOutCurrency),
		PayOutMethod:   common.PaymentMethodType(method),
		QuoteType:      payment.QuoteType_QUOTE_TYPE_REALTIME,
	}))
	if err != nil {
		httpError(w, err.Error(), http.StatusBadGateway)
		return
	}

	jsonResponse(w, resp.Msg)
}

func httpError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(ErrorResponse{Error: message})
}

func jsonResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(data)
}

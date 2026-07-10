package recipient

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	"my-provider/internal/paymentintent"
)

var validate = validator.New(validator.WithRequiredStructEnabled())

// APIHandler exposes admin REST endpoints for the beneficiary (recipient) role.
type APIHandler struct {
	store         paymentintent.Store
	networkClient NetworkClient
	apiKeys       map[string]struct{}
}

// NewAPIHandler creates a new admin REST handler for the recipient role.
func NewAPIHandler(store paymentintent.Store, networkClient NetworkClient, apiKeys []string) *APIHandler {
	keySet := make(map[string]struct{}, len(apiKeys))
	for _, k := range apiKeys {
		keySet[k] = struct{}{}
	}
	return &APIHandler{store: store, networkClient: networkClient, apiKeys: keySet}
}

// Router returns the API mux. Patterns are relative to the /api/v1/ mount point.
func (h *APIHandler) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /payment-intent-quotes", h.withAuth(h.handleGetQuote))
	mux.HandleFunc("POST /payment-intents", h.withAuth(h.handleCreatePaymentIntent))
	return mux
}

func (h *APIHandler) withAuth(next http.HandlerFunc) http.HandlerFunc {
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

func (h *APIHandler) handleGetQuote(w http.ResponseWriter, r *http.Request) {
	var req GetQuoteHTTPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	if err := validate.Struct(req); err != nil {
		httpError(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	resp, err := GetQuote(r.Context(), h.networkClient, GetQuoteRequest{
		PayInCurrency:       req.PayInCurrency,
		PayInAmount:         req.PayInAmount.toDecimal(),
		PayOutCurrency:      req.PayOutCurrency,
		PayInPaymentMethod:  req.PayInPaymentMethod,
		PayOutPaymentMethod: req.PayOutPaymentMethod,
	})
	if err != nil {
		httpError(w, fmt.Sprintf("network error: %v", err), http.StatusBadGateway)
		return
	}

	h.jsonResponse(w, http.StatusOK, resp)
}

func (h *APIHandler) handleCreatePaymentIntent(w http.ResponseWriter, r *http.Request) {
	var req CreatePaymentIntentHTTPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}
	if err := validate.Struct(req); err != nil {
		httpError(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	resp, err := CreatePaymentIntent(r.Context(), h.networkClient, CreatePaymentIntentRequest{
		PaymentReference: req.PaymentReference,
		PayInCurrency:    req.PayInCurrency,
		PayInAmount:      req.PayInAmount.toDecimal(),
		PayOutCurrency:   req.PayOutCurrency,
		PayOutDetails:    req.PayOutDetails,
	})
	if err != nil {
		httpError(w, fmt.Sprintf("network error: %v", err), http.StatusBadGateway)
		return
	}

	// Persist a local beneficiary record for status tracking.
	if resp.PaymentIntentId != 0 {
		pi := paymentintent.PaymentIntent{
			ID:               resp.PaymentIntentId,
			Role:             paymentintent.RoleBeneficiary,
			Currency:         req.PayInCurrency,
			Amount:           req.PayInAmount.toDecimal(),
			PaymentReference: req.PaymentReference,
			PayoutCurrency:   req.PayOutCurrency,
			Status:           paymentintent.StatusCreated,
		}
		if _, _, err := h.store.GetOrCreate(r.Context(), pi); err != nil {
			httpError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	h.jsonResponse(w, http.StatusCreated, resp)
}

// GetQuoteHTTPRequest is the JSON body for POST /api/v1/payment-intent-quotes.
type GetQuoteHTTPRequest struct {
	PayInCurrency       string          `json:"payInCurrency" validate:"required,len=3,uppercase"`
	PayInAmount         APIDecimal      `json:"payInAmount" validate:"required"`
	PayOutCurrency      string          `json:"payOutCurrency" validate:"required,len=3,uppercase"`
	PayInPaymentMethod  string          `json:"payInPaymentMethod" validate:"required"`
	PayOutPaymentMethod string          `json:"payOutPaymentMethod" validate:"required"`
}

// CreatePaymentIntentHTTPRequest is the JSON body for POST /api/v1/payment-intents.
type CreatePaymentIntentHTTPRequest struct {
	PaymentReference string                `json:"paymentReference" validate:"required"`
	PayInCurrency    string                `json:"payInCurrency" validate:"required,len=3,uppercase"`
	PayInAmount      APIDecimal            `json:"payInAmount" validate:"required"`
	PayOutCurrency   string                `json:"payOutCurrency" validate:"required,len=3,uppercase"`
	PayOutDetails    *common.PaymentDetails `json:"payOutDetails" validate:"required"`
}

// APIDecimal mirrors paymentintent.Decimal for JSON binding.
type APIDecimal struct {
	Unscaled int64 `json:"unscaled" validate:"required"`
	Exponent int32 `json:"exponent"`
}

func (d APIDecimal) toDecimal() *paymentintent.Decimal {
	return &paymentintent.Decimal{Unscaled: d.Unscaled, Exponent: d.Exponent}
}

func httpError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (h *APIHandler) jsonResponse(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}

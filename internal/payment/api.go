// Package payment exposes REST endpoints for the payout payment lifecycle.
package payment

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
	sdkpayment "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
)

var validate = validator.New(validator.WithRequiredStructEnabled())

// Handler exposes the payment REST API.
type Handler struct {
	store   Store
	client  *NetworkClient
	apiKeys map[string]struct{}
}

// NewHandler creates a new payment REST handler.
func NewHandler(store Store, client *NetworkClient, apiKeys []string) *Handler {
	keySet := make(map[string]struct{}, len(apiKeys))
	for _, k := range apiKeys {
		keySet[k] = struct{}{}
	}
	return &Handler{store: store, client: client, apiKeys: keySet}
}

// Router returns the API mux.
func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/payments", h.withAuth(h.handleCreatePayment))
	mux.HandleFunc("GET /api/v1/payments/{id}", h.withAuth(h.handleGetPayment))
	mux.HandleFunc("POST /api/v1/payments/{id}/aml/approve", h.withAuth(h.handleAmlApprove))
	mux.HandleFunc("POST /api/v1/payments/{id}/aml/reject", h.withAuth(h.handleAmlReject))
	mux.HandleFunc("POST /api/v1/payments/{id}/finalize", h.withAuth(h.handleFinalizePayment))
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

func (h *Handler) handleCreatePayment(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if err := validate.Struct(req); err != nil {
		httpError(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Idempotency: if paymentClientId already exists, return existing record.
	if existing, err := h.store.GetByPaymentClientID(r.Context(), req.PaymentClientID); err == nil {
		h.jsonResponse(w, http.StatusOK, existing)
		return
	} else if !errors.Is(err, ErrNotFound) {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	p := Payment{
		PaymentClientID:    req.PaymentClientID,
		Role:               RoleOFI,
		Status:             StatusCreated,
		PayoutCurrency:     req.Currency,
		PayoutMethod:       req.PaymentMethod,
		PaymentDetailsJSON: string(req.PaymentDetails),
		TravelRuleDataJSON: string(req.TravelRuleData),
	}
	if req.QuoteID != nil {
		qid := req.QuoteID.QuoteID
		pid := req.QuoteID.ProviderID
		p.QuoteID = &qid
		p.ProviderID = &pid
	}

	if err := p.Validate(); err != nil {
		httpError(w, err.Error(), http.StatusBadRequest)
		return
	}

	id, err := h.store.Create(r.Context(), p)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Call network asynchronously is an option, but for REST semantics we
	// call synchronously so the caller sees immediate success/failure.
	resp, err := h.client.CreatePayment(r.Context(), req)
	if err != nil {
		_ = h.store.UpdateFailed(r.Context(), id, err.Error())
		httpError(w, fmt.Sprintf("network error: %v", err), http.StatusBadGateway)
		return
	}

	switch result := resp.Result.(type) {
	case *sdkpayment.CreatePaymentResponse_Accepted_:
		acc := result.Accepted
		pid := acc.PaymentId
		ppid := acc.PayoutProviderId
		payoutAmount := fromCommonDecimal(acc.PayoutAmount)
		if err := h.store.UpdatePayoutRequest(r.Context(), id, pid, ppid); err != nil {
			httpError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if payoutAmount != nil {
			if err := h.store.UpdateAccepted(r.Context(), id, payoutAmount); err != nil {
				httpError(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	case *sdkpayment.CreatePaymentResponse_Failure_:
		_ = h.store.UpdateFailed(r.Context(), id, result.Failure.String())
	case *sdkpayment.CreatePaymentResponse_SettlementRequired_:
		// Payment is accepted but requires prefunding; keep status as CREATED
		// so the caller can settle and retry.
	}

	updated, err := h.store.GetByID(r.Context(), id)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.jsonResponse(w, http.StatusCreated, updated)
}

func (h *Handler) handleGetPayment(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		httpError(w, "invalid payment id", http.StatusBadRequest)
		return
	}

	p, err := h.store.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpError(w, "payment not found", http.StatusNotFound)
			return
		}
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.jsonResponse(w, http.StatusOK, p)
}

func (h *Handler) handleAmlApprove(w http.ResponseWriter, r *http.Request) {
	h.handleAmlDecision(w, r, true)
}

func (h *Handler) handleAmlReject(w http.ResponseWriter, r *http.Request) {
	h.handleAmlDecision(w, r, false)
}

func (h *Handler) handleAmlDecision(w http.ResponseWriter, r *http.Request, approved bool) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		httpError(w, "invalid payment id", http.StatusBadRequest)
		return
	}

	p, err := h.store.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpError(w, "payment not found", http.StatusNotFound)
			return
		}
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if p.PaymentID == nil {
		httpError(w, "payment has no network payment id", http.StatusBadRequest)
		return
	}

	var reason string
	if !approved {
		var req struct {
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
			return
		}
		reason = req.Reason
	}

	if err := h.client.CompleteManualAmlCheck(r.Context(), *p.PaymentID, approved, reason); err != nil {
		httpError(w, fmt.Sprintf("network error: %v", err), http.StatusBadGateway)
		return
	}

	if !approved {
		if err := h.store.UpdateFailed(r.Context(), id, reason); err != nil {
			httpError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	p, _ = h.store.GetByID(r.Context(), id)
	h.jsonResponse(w, http.StatusOK, p)
}

func (h *Handler) handleFinalizePayment(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		httpError(w, "invalid payment id", http.StatusBadRequest)
		return
	}

	p, err := h.store.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpError(w, "payment not found", http.StatusNotFound)
			return
		}
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if p.PaymentID == nil {
		httpError(w, "payment has no network payment id", http.StatusBadRequest)
		return
	}

	var req FinalizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if err := h.client.FinalizePayout(r.Context(), *p.PaymentID, req); err != nil {
		httpError(w, fmt.Sprintf("network error: %v", err), http.StatusBadGateway)
		return
	}

	if err := h.store.UpdateFinalize(r.Context(), id, req.PayoutID, req.Receipt); err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	p, _ = h.store.GetByID(r.Context(), id)
	h.jsonResponse(w, http.StatusOK, p)
}

func httpError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func (h *Handler) jsonResponse(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}

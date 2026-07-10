package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	"my-provider/internal/paymentintent"
)

// APIHandler exposes admin REST endpoints for the Pay-In Provider role.
type APIHandler struct {
	store         paymentintent.Store
	networkClient NetworkClient
	apiKeys       map[string]struct{}
}

// NewAPIHandler creates a new admin REST handler.
func NewAPIHandler(store paymentintent.Store, networkClient NetworkClient, apiKeys []string) *APIHandler {
	keySet := make(map[string]struct{}, len(apiKeys))
	for _, k := range apiKeys {
		keySet[k] = struct{}{}
	}
	return &APIHandler{store: store, networkClient: networkClient, apiKeys: keySet}
}

// Router returns the API mux. Patterns are relative to the /api/v1/payment-intents/provider/ mount point.
func (h *APIHandler) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{id}", h.withAuth(h.handleGet))
	mux.HandleFunc("POST /{id}/confirm", h.withAuth(h.handleConfirm))
	mux.HandleFunc("POST /{id}/reject", h.withAuth(h.handleReject))
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

func (h *APIHandler) handleGet(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		httpError(w, err.Error(), http.StatusBadRequest)
		return
	}
	pi, err := h.store.Get(r.Context(), id, paymentintent.RolePayInProvider)
	if err != nil {
		if err == paymentintent.ErrNotFound {
			httpError(w, "payment intent not found", http.StatusNotFound)
			return
		}
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.jsonResponse(w, http.StatusOK, ToResponse(pi, nil))
}

func (h *APIHandler) handleConfirm(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		httpError(w, err.Error(), http.StatusBadRequest)
		return
	}
	pi, err := h.store.Get(r.Context(), id, paymentintent.RolePayInProvider)
	if err != nil {
		if err == paymentintent.ErrNotFound {
			httpError(w, "payment intent not found", http.StatusNotFound)
			return
		}
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	method := common.PaymentMethodType_PAYMENT_METHOD_TYPE_SEPA
	if pi.PaymentMethod != "" {
		method = common.PaymentMethodType(common.PaymentMethodType_value[pi.PaymentMethod])
	}

	resp, err := ConfirmPayment(r.Context(), h.networkClient, id, method)
	if err != nil {
		httpError(w, fmt.Sprintf("network error: %v", err), http.StatusBadGateway)
		return
	}

	if err := h.store.MarkFundsReceived(r.Context(), id, paymentintent.RolePayInProvider, time.Now(), resp.PayoutProviderId); err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pi, _ = h.store.Get(r.Context(), id, paymentintent.RolePayInProvider)
	h.jsonResponse(w, http.StatusOK, ToResponse(pi, resp))
}

func (h *APIHandler) handleReject(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		httpError(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if err := RejectPaymentIntent(r.Context(), h.networkClient, id, req.Reason); err != nil {
		httpError(w, fmt.Sprintf("network error: %v", err), http.StatusBadGateway)
		return
	}

	if err := h.store.MarkRejected(r.Context(), id, paymentintent.RolePayInProvider, req.Reason, time.Now()); err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pi, _ := h.store.Get(r.Context(), id, paymentintent.RolePayInProvider)
	h.jsonResponse(w, http.StatusOK, ToResponse(pi, nil))
}

func parseID(s string) (uint64, error) {
	id, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid id")
	}
	return id, nil
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

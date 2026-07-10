package settlement

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// APIHandler exposes REST endpoints for settlement data.
type APIHandler struct {
	store   Store
	apiKeys map[string]struct{}
}

// NewAPIHandler creates a new settlement REST handler.
func NewAPIHandler(store Store, apiKeys []string) *APIHandler {
	keySet := make(map[string]struct{}, len(apiKeys))
	for _, k := range apiKeys {
		keySet[k] = struct{}{}
	}
	return &APIHandler{store: store, apiKeys: keySet}
}

// Router returns the API mux.
func (h *APIHandler) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/settlement/credits", h.withAuth(h.handleGetCredits))
	mux.HandleFunc("GET /api/v1/settlement/credits/{counterpartId}", h.withAuth(h.handleGetCredit))
	mux.HandleFunc("GET /api/v1/settlement/ledger", h.withAuth(h.handleGetLedger))
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

func (h *APIHandler) handleGetCredits(w http.ResponseWriter, r *http.Request) {
	limits, err := h.store.GetCreditLimits(r.Context())
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.jsonResponse(w, http.StatusOK, map[string]any{"credits": limits})
}

func (h *APIHandler) handleGetCredit(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("counterpartId")
	id, err := strconv.ParseInt(idStr, 10, 32)
	if err != nil {
		httpError(w, "invalid counterpart id", http.StatusBadRequest)
		return
	}
	cl, err := h.store.GetCreditLimit(r.Context(), int32(id))
	if err != nil {
		if err == ErrNotFound {
			httpError(w, "credit limit not found", http.StatusNotFound)
			return
		}
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.jsonResponse(w, http.StatusOK, cl)
}

func (h *APIHandler) handleGetLedger(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
			if limit > 1000 {
				limit = 1000
			}
		}
	}
	entries, err := h.store.GetLedgerEntries(r.Context(), limit)
	if err != nil {
		httpError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.jsonResponse(w, http.StatusOK, map[string]any{"entries": entries})
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

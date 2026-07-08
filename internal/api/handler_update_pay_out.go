package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"connectrpc.com/connect"

	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
)

// networkClientIface is the subset of paymentconnect.NetworkServiceClient
// we use. Defining a local interface makes the handler trivially
// stubbable in tests without dragging in connect internals.
type networkClientIface interface {
	UpdateQuote(context.Context, *connect.Request[payment.UpdateQuoteRequest]) (*connect.Response[payment.UpdateQuoteResponse], error)
}

// Handler is the HTTP handler for POST /api/v1/quotes/pay-out.
type Handler struct {
	networkClient  networkClientIface
	idem           *IdempotencyStore
	upstreamTimeout time.Duration
	maxBodyBytes    int64
}

// NewHandler constructs an HTTP handler with the given deps.
func NewHandler(nc networkClientIface, idem *IdempotencyStore, upstreamTimeout time.Duration, maxBodyBytes int64) *Handler {
	return &Handler{
		networkClient:   nc,
		idem:            idem,
		upstreamTimeout: upstreamTimeout,
		maxBodyBytes:    maxBodyBytes,
	}
}

// ServeHTTP implements http.Handler. Order of operations:
//   1. Parse JSON body (limit by maxBodyBytes)
//   2. Validate business rules (returns 4xx with specific code)
//   3. Idempotency lookup (returns cached or 409 on conflict)
//   4. Build SDK request and call networkClient.UpdateQuote with timeout
//   5. Map upstream error to HTTP (502/504/422/409)
//   6. Cache success response and return
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := RequestIDFromContext(r.Context())
	apiKey := APIKeyFromContext(r.Context())

	// 1. Parse JSON.
	var req UpdatePayOutRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, h.maxBodyBytes))
	if err := dec.Decode(&req); err != nil {
		writeError(w, 400, "invalid_request", "malformed JSON: "+err.Error(), requestID)
		return
	}

	// 2. Validate.
	if verr := req.Validate(); verr != nil {
		writeError(w, verr.HTTPStatus, verr.Code, verr.Detail, requestID)
		return
	}

	// 3. Idempotency.
	idemKey := r.Header.Get("Idempotency-Key")
	bodyHash := sha256.Sum256([]byte(req.canonicalString()))
	if idemKey == "" {
		// No client-provided key — synthesize one from body hash so retried
		// identical requests still hit the cache.
		idemKey = "auto-" + hex.EncodeToString(bodyHash[:16])
	}
	if rec, ok := h.idem.Lookup(apiKey, idemKey); ok {
		if rec.BodyHash != bodyHash {
			writeError(w, 409, "idempotency_conflict",
				"idempotency key already used with different body", requestID)
			return
		}
		// Replay: return cached response.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Idempotent-Replay", "true")
		w.WriteHeader(rec.Status)
		_, _ = w.Write(rec.Response)
		return
	}

	// 4. Build & call.
	sdkReq := req.ToSDKRequest()
	ctx, cancel := context.WithTimeout(r.Context(), h.upstreamTimeout)
	defer cancel()
	_, err := h.networkClient.UpdateQuote(ctx, connect.NewRequest(sdkReq))

	// 5. Map error.
	if err != nil {
		apiErr := MapError(err)
		writeError(w, apiErr.HTTPStatus, apiErr.Code, apiErr.Detail, requestID)
		return
	}

	// 6. Success: build response, cache, send.
	maxExpSec := int32(0)
	for _, g := range req.Groups {
		if g.ExpirationSeconds > maxExpSec {
			maxExpSec = g.ExpirationSeconds
		}
	}
	now := time.Now().UTC()
	bandsCount := 0
	for _, g := range req.Groups {
		bandsCount += len(g.Bands)
	}
	resp := UpdatePayOutResponse{
		Status:          "OK",
		AppliedAt:       now.Format(time.RFC3339Nano),
		ExpiresAt:       now.Add(time.Duration(maxExpSec) * time.Second).Format(time.RFC3339Nano),
		GroupsPublished: len(req.Groups),
		BandsPublished:  bandsCount,
		RequestID:       requestID,
	}
	body, _ := json.Marshal(resp)

	ttl := max2xExp(maxExpSec, 60*time.Second)
	h.idem.Save(apiKey, idemKey, bodyHash, 200, body, ttl)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}

// max2xExp returns max(2*seconds, minTTL).
func max2xExp(seconds int32, minTTL time.Duration) time.Duration {
	d := 2 * time.Duration(seconds) * time.Second
	if d < minTTL {
		return minTTL
	}
	return d
}
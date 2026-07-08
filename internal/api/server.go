package api

import (
	"context"
	"net/http"
	"time"

	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
)

// Deps bundles the dependencies the API router needs. The router is the
// only thing cmd/main.go constructs; everything else is wired here.
type Deps struct {
	NetworkClient   paymentconnect.NetworkServiceClient
	APIKeys         []string
	MaxBodyBytes    int64         // default 64 << 10
	RequestsPerSec  float64       // default 20
	Burst           int           // default 40
	UpstreamTimeout time.Duration // default 5s
	IdempotencyTTL  time.Duration // floor; per-request TTL is max(2*expiration, this)
}

// NewRouter builds the http.Handler for the entire /api/v1/ tree.
//
// Middleware order (outer to inner):
//   1. RequestID  — generates X-Request-Id, propagates via context
//   2. Recover    — turns panics into 500s
//   3. MaxBody    — caps request body before any parsing
//   4. Auth       — Bearer token check
//   5. RateLimit  — per-key token bucket
//   6. (handler)
//
// The returned handler is meant to be mounted under "/api/v1/" by the
// caller; paths in tests should be relative to that prefix (e.g.
// "/quotes/pay-out").
func NewRouter(d Deps) http.Handler {
	if d.MaxBodyBytes <= 0 {
		d.MaxBodyBytes = 64 << 10
	}
	if d.RequestsPerSec <= 0 {
		d.RequestsPerSec = 20
	}
	if d.Burst <= 0 {
		d.Burst = 40
	}
	if d.UpstreamTimeout <= 0 {
		d.UpstreamTimeout = 5 * time.Second
	}
	if d.IdempotencyTTL <= 0 {
		d.IdempotencyTTL = 60 * time.Second
	}

	idem := NewIdempotencyStore()
	rl := NewRateLimiter(d.RequestsPerSec, d.Burst)
	h := NewHandler(d.NetworkClient, idem, d.UpstreamTimeout, d.MaxBodyBytes)

	mux := http.NewServeMux()
	// Use exact match (no trailing slash) — we have only one route.
	mux.Handle("/api/v1/quotes/pay-out", h)

	chain := RequestIDMiddleware(
		RecoverMiddleware(
			MaxBodyMiddleware(d.MaxBodyBytes)(
				AuthMiddleware(d.APIKeys)(
					RateLimitMiddleware(rl)(mux),
				),
			),
		),
	)
	return chain
}

// silence unused-import warning when this file is the only consumer.
var _ = context.Background
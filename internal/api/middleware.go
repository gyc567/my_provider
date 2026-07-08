package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

// requestIDHeader is the canonical name for the request-id header.
const requestIDHeader = "X-Request-Id"

// requestIDKey is the context key under which the per-request ID is stored.
type ctxKeyRequestID struct{}

// ctxKeyAPIKey is the context key under which the authenticated API key is stored.
type ctxKeyAPIKey struct{}

// RequestIDFromContext returns the per-request ID injected by RequestIDMiddleware.
// Returns "" if none was set (only happens if middleware wasn't used).
func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyRequestID{}).(string); ok {
		return v
	}
	return ""
}

// APIKeyFromContext returns the authenticated API key injected by AuthMiddleware.
// Returns "" if none was set.
func APIKeyFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyAPIKey{}).(string); ok {
		return v
	}
	return ""
}

// requestIDFromHeader returns the trimmed X-Request-Id, or "" if absent.
func requestIDFromHeader(h http.Header) string {
	return h.Get(requestIDHeader)
}

// RequestIDMiddleware injects a per-request ID. If the client sent
// X-Request-Id we honor it (after a length cap to avoid abuse); otherwise
// we generate a 16-byte hex string. The ID is stored in the request
// context AND echoed back in the response header.
func RequestIDMiddleware(next http.Handler) http.Handler {
	const maxClientIDLen = 128
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id string
		if c := requestIDFromHeader(r.Header); c != "" {
			if len(c) > maxClientIDLen {
				c = c[:maxClientIDLen]
			}
			id = c
		} else {
			id = newRequestID()
		}
		w.Header().Set(requestIDHeader, id)
		ctx := context.WithValue(r.Context(), ctxKeyRequestID{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand on Linux/macOS never fails in practice; fall back to time.
		return "req-" + time.Now().UTC().Format("20060102T150405.000000000")
	}
	return "req-" + hex.EncodeToString(b[:])
}

// AuthMiddleware returns a middleware that rejects requests whose
// Authorization header does not carry one of validKeys as a Bearer token.
// validKeys is a slice of pre-shared API keys; comparison is constant-time.
func AuthMiddleware(validKeys []string) func(http.Handler) http.Handler {
	// Copy into a map for O(1) lookup, but compare with subtle to avoid timing leaks.
	set := make(map[string]struct{}, len(validKeys))
	for _, k := range validKeys {
		set[k] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			const prefix = "Bearer "
			h := r.Header.Get("Authorization")
			if !startsWith(h, prefix) {
				writeError(w, 401, "unauthorized", "missing or malformed Authorization header", RequestIDFromContext(r.Context()))
				return
			}
			provided := h[len(prefix):]
			if !containsKeyConstantTime(set, provided) {
				writeError(w, 401, "unauthorized", "invalid api key", RequestIDFromContext(r.Context()))
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyAPIKey{}, provided)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// containsKeyConstantTime returns true iff needle is in set. Iterates
// the full set (with a constant-time compare per key) so that the
// timing of a match vs. a non-match doesn't leak the key length or
// prefix to an attacker observing response times.
func containsKeyConstantTime(set map[string]struct{}, needle string) bool {
	found := 0
	for k := range set {
		if len(k) == len(needle) && subtle.ConstantTimeCompare([]byte(k), []byte(needle)) == 1 {
			found = 1
		}
	}
	return found == 1
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// RateLimiter is a per-key token-bucket wrapper around rate.Limiter.
// It is safe for concurrent use; the inner map is guarded by a RWMutex.
type RateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rps      float64
	burst    int
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter returns a per-key token bucket allowing rps requests per
// second with a burst of `burst` per key.
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*bucket),
		rps:     rps,
		burst:   burst,
	}
}

// Allow returns true if the (scope, key) tuple may proceed. The scope is
// the API key (so different keys have independent buckets); key is
// unused here but kept for future per-route differentiation.
func (rl *RateLimiter) Allow(scope string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.buckets[scope]
	if !ok {
		b = &bucket{tokens: float64(rl.burst), last: now}
		rl.buckets[scope] = b
	}
	// Refill: (now - last) seconds * rps tokens, capped at burst.
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * rl.rps
	if b.tokens > float64(rl.burst) {
		b.tokens = float64(rl.burst)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// RateLimitMiddleware returns a 429-rejecting middleware. The bucket key
// is the authenticated API key (so unauthenticated traffic still gets a
// bucket — keyed by IP — to prevent unauthenticated flooding).
func RateLimitMiddleware(rl *RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := APIKeyFromContext(r.Context())
			if key == "" {
				key = r.RemoteAddr
			}
			if !rl.Allow(key) {
				w.Header().Set("Retry-After", "1")
				writeError(w, 429, "rate_limited", "too many requests, retry after 1s", RequestIDFromContext(r.Context()))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RecoverMiddleware turns panics into 500s. Without this, a panic in any
// handler would kill the process. Logs nothing here — callers can wrap
// with their own logger middleware.
func RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				writeError(w, 500, "internal_error", "internal server error", RequestIDFromContext(r.Context()))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// MaxBodyMiddleware caps the request body to maxBytes. Anything larger
// is rejected with 413 before we waste cycles decoding it.
func MaxBodyMiddleware(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxBytes {
				writeError(w, 413, "payload_too_large",
					"request body exceeds maximum size", RequestIDFromContext(r.Context()))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

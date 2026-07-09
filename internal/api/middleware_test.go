package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRequestIDMiddleware_GeneratesWhenAbsent(t *testing.T) {
	t.Parallel()
	h := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := RequestIDFromContext(r.Context())
		if id == "" {
			t.Error("expected request id in context")
		}
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, req)
	got := rec.Header().Get(requestIDHeader)
	if got == "" {
		t.Error("expected X-Request-Id in response header")
	}
	if !strings.HasPrefix(got, "req-") {
		t.Errorf("expected generated id to start with 'req-', got %q", got)
	}
}

func TestRequestIDMiddleware_RespectsClientHeader(t *testing.T) {
	t.Parallel()
	h := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := RequestIDFromContext(r.Context()); got != "client-supplied-id" {
			t.Errorf("got id %q, want client-supplied-id", got)
		}
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(requestIDHeader, "client-supplied-id")
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get(requestIDHeader); got != "client-supplied-id" {
		t.Errorf("response header: got %q, want client-supplied-id", got)
	}
}

func TestRequestIDMiddleware_TruncatesVeryLongClientHeader(t *testing.T) {
	t.Parallel()
	h := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := RequestIDFromContext(r.Context())
		if len(id) > 128 {
			t.Errorf("id length %d exceeds 128", len(id))
		}
	}))
	long := strings.Repeat("a", 1000)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(requestIDHeader, long)
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get(requestIDHeader); len(got) > 128 {
		t.Errorf("response id length %d exceeds 128", len(got))
	}
}

func TestAuthMiddleware_AcceptsValidKey(t *testing.T) {
	t.Parallel()
	h := AuthMiddleware([]string{"key1", "key2"})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := APIKeyFromContext(r.Context()); got != "key1" {
				t.Errorf("ctx key: got %q, want key1", got)
			}
			w.WriteHeader(200)
		}),
	)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

func TestAuthMiddleware_RejectsMissingHeader(t *testing.T) {
	t.Parallel()
	h := AuthMiddleware([]string{"key1"})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("handler should not be called")
		}),
	)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unauthorized") {
		t.Errorf("body: got %q, want contains 'unauthorized'", rec.Body.String())
	}
}

func TestAuthMiddleware_RejectsWrongKey(t *testing.T) {
	t.Parallel()
	h := AuthMiddleware([]string{"key1"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

func TestAuthMiddleware_RejectsMalformedHeader(t *testing.T) {
	t.Parallel()
	h := AuthMiddleware([]string{"key1"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))
	for _, h2 := range []string{"key1", "Basic key1", "key1"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", h2)
		h.ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Errorf("auth=%q: status %d, want 401", h2, rec.Code)
		}
	}
}

func TestRateLimiter_AllowsUpToBurst(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter(1, 5) // 1 rps, burst 5
	for i := 0; i < 5; i++ {
		if !rl.Allow("k1") {
			t.Errorf("call %d should be allowed within burst", i)
		}
	}
	if rl.Allow("k1") {
		t.Error("6th call should be rate-limited")
	}
}

func TestRateLimiter_DifferentKeysIndependent(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter(1, 2)
	if !rl.Allow("k1") { t.Error("k1 first should pass") }
	if !rl.Allow("k1") { t.Error("k1 second should pass") }
	if rl.Allow("k1")   { t.Error("k1 third should fail") }
	if !rl.Allow("k2") { t.Error("k2 first should pass (independent bucket)") }
}

func TestRateLimiter_Refills(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter(20, 1) // 20 rps = 50ms per token
	if !rl.Allow("k1") { t.Fatal("first should pass") }
	if rl.Allow("k1")   { t.Fatal("second should fail (burst=1)") }
	time.Sleep(60 * time.Millisecond)
	if !rl.Allow("k1") { t.Error("after 60ms, should refill") }
}

func TestRateLimitMiddleware_429(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter(1, 1)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := AuthMiddleware([]string{"k1"})(RateLimitMiddleware(rl)(inner))
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer k1")
		h.ServeHTTP(rec, req)
		if i == 0 && rec.Code != 200 {
			t.Errorf("first call: got %d, want 200", rec.Code)
		}
		if i == 1 && rec.Code != 429 {
			t.Errorf("second call: got %d, want 429", rec.Code)
		}
		if i == 1 && rec.Header().Get("Retry-After") == "" {
			t.Error("429 should set Retry-After")
		}
	}
}

func TestRecoverMiddleware_500OnPanic(t *testing.T) {
	t.Parallel()
	h := RecoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != 500 {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal_error") {
		t.Errorf("body: got %q, want contains 'internal_error'", rec.Body.String())
	}
}

func TestRecoverMiddleware_PassesThrough(t *testing.T) {
	t.Parallel()
	h := RecoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
}

func TestMaxBodyMiddleware_RejectsLargeContentLength(t *testing.T) {
	t.Parallel()
	h := MaxBodyMiddleware(100)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called for oversized body")
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", bytes.NewReader(bytes.Repeat([]byte("a"), 200)))
	req.ContentLength = 200
	h.ServeHTTP(rec, req)
	if rec.Code != 413 {
		t.Errorf("status: got %d, want 413", rec.Code)
	}
}

func TestMaxBodyMiddleware_AllowsSmallBody(t *testing.T) {
	t.Parallel()
	called := false
	h := MaxBodyMiddleware(1000)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", bytes.NewReader([]byte("hello")))
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if !called {
		t.Error("handler should be called")
	}
}

// Ensure no data race between concurrent ServeHTTP calls.
func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	rl := NewRateLimiter(100, 50)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rl.Allow("k1")
		}()
	}
	wg.Wait()
}

// context propagation sanity check.
func TestRequestIDFromContext_Empty(t *testing.T) {
	t.Parallel()
	if got := RequestIDFromContext(context.Background()); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

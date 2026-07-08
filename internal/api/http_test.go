package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
)

// fakeNetworkClient is a stub for paymentconnect.NetworkServiceClient.
// Only UpdateQuote is exercised in handler tests; the other methods
// return "not implemented" to satisfy the interface.
type fakeNetworkClient struct {
	updateQuoteFn func(context.Context, *connect.Request[payment.UpdateQuoteRequest]) (*connect.Response[payment.UpdateQuoteResponse], error)

	calls atomic.Int64
	mu    sync.Mutex
	last  *payment.UpdateQuoteRequest
}

func (f *fakeNetworkClient) UpdateQuote(ctx context.Context, req *connect.Request[payment.UpdateQuoteRequest]) (*connect.Response[payment.UpdateQuoteResponse], error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.last = req.Msg
	f.mu.Unlock()
	if f.updateQuoteFn != nil {
		return f.updateQuoteFn(ctx, req)
	}
	return connect.NewResponse(&payment.UpdateQuoteResponse{}), nil
}

func (f *fakeNetworkClient) GetQuote(context.Context, *connect.Request[payment.GetQuoteRequest]) (*connect.Response[payment.GetQuoteResponse], error) {
	return nil, errors.New("not implemented")
}
func (f *fakeNetworkClient) CreatePayment(context.Context, *connect.Request[payment.CreatePaymentRequest]) (*connect.Response[payment.CreatePaymentResponse], error) {
	return nil, errors.New("not implemented")
}
func (f *fakeNetworkClient) ConfirmPayout(context.Context, *connect.Request[payment.ConfirmPayoutRequest]) (*connect.Response[payment.ConfirmPayoutResponse], error) {
	return nil, errors.New("not implemented")
}
func (f *fakeNetworkClient) FinalizePayout(context.Context, *connect.Request[payment.FinalizePayoutRequest]) (*connect.Response[payment.FinalizePayoutResponse], error) {
	return nil, errors.New("not implemented")
}
func (f *fakeNetworkClient) CompleteManualAmlCheck(context.Context, *connect.Request[payment.CompleteManualAmlCheckRequest]) (*connect.Response[payment.CompleteManualAmlCheckResponse], error) {
	return nil, errors.New("not implemented")
}

var _ paymentconnect.NetworkServiceClient = (*fakeNetworkClient)(nil)

func newTestHandler(fake *fakeNetworkClient, opts ...func(*Deps)) http.Handler {
	d := Deps{
		NetworkClient:   fake,
		APIKeys:         []string{"key1"},
		MaxBodyBytes:    64 << 10,
		RequestsPerSec:  1000, // effectively unlimited for tests
		Burst:           1000,
		UpstreamTimeout: 1 * time.Second,
		IdempotencyTTL:  60 * time.Second,
	}
	for _, o := range opts {
		o(&d)
	}
	return NewRouter(d)
}

func validBody() string {
	return `{
		"groups": [{
			"currency": "EUR",
			"payment_method": "SEPA",
			"expiration_seconds": 30,
			"bands": [
				{"client_quote_id":"c1","max_amount_usd":"1000","rate":"0.86"},
				{"client_quote_id":"c2","max_amount_usd":"10000","rate":"0.87"}
			]
		}]
	}`
}

func TestHandler_200_HappyPath(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkClient{}
	h := newTestHandler(fake)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(validBody()))
	req.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status: got %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if fake.calls.Load() != 1 {
		t.Errorf("expected 1 upstream call, got %d", fake.calls.Load())
	}
	var resp UpdatePayOutResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode resp: %v", err)
	}
	if resp.Status != "OK" {
		t.Errorf("status: got %q, want OK", resp.Status)
	}
	if resp.GroupsPublished != 1 {
		t.Errorf("groups_published: got %d, want 1", resp.GroupsPublished)
	}
	if resp.BandsPublished != 2 {
		t.Errorf("bands_published: got %d, want 2", resp.BandsPublished)
	}
	if resp.AppliedAt == "" || resp.ExpiresAt == "" {
		t.Errorf("timestamps should be set")
	}
}

func TestHandler_200_PassesGroupsToSDK(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkClient{}
	h := newTestHandler(fake)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(validBody()))
	req.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status: got %d", rec.Code)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.last == nil {
		t.Fatal("expected last request captured")
	}
	if len(fake.last.PayOut) != 1 {
		t.Fatalf("expected 1 PayOut group, got %d", len(fake.last.PayOut))
	}
	g := fake.last.PayOut[0]
	if g.Currency != "EUR" {
		t.Errorf("currency: got %q, want EUR", g.Currency)
	}
	if g.PaymentMethod != 10 { // SEPA = 10
		t.Errorf("payment_method: got %d, want 10 (SEPA)", g.PaymentMethod)
	}
	if len(g.Bands) != 2 {
		t.Fatalf("expected 2 bands, got %d", len(g.Bands))
	}
	if g.Bands[0].ClientQuoteId != "c1" {
		t.Errorf("band 0 client_quote_id: got %q, want c1", g.Bands[0].ClientQuoteId)
	}
	// max_amount should be a Decimal with Unscaled=1000, Exponent=0
	if g.Bands[0].MaxAmount.Unscaled != 1000 || g.Bands[0].MaxAmount.Exponent != 0 {
		t.Errorf("band 0 max_amount: got {%d, %d}, want {1000, 0}",
			g.Bands[0].MaxAmount.Unscaled, g.Bands[0].MaxAmount.Exponent)
	}
	// PayIn must always be nil
	if fake.last.PayIn != nil {
		t.Errorf("PayIn should be nil, got %v", fake.last.PayIn)
	}
}

func TestHandler_400_MalformedJSON(t *testing.T) {
	t.Parallel()
	h := newTestHandler(&fakeNetworkClient{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader("not json"))
	req.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_request") {
		t.Errorf("body: got %q", rec.Body.String())
	}
}

func TestHandler_400_UnsupportedBand(t *testing.T) {
	t.Parallel()
	h := newTestHandler(&fakeNetworkClient{})
	body := `{"groups":[{"currency":"EUR","payment_method":"SEPA","expiration_seconds":30,"bands":[{"client_quote_id":"c1","max_amount_usd":"2000","rate":"0.86"}]}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unsupported_band") {
		t.Errorf("body: got %q", rec.Body.String())
	}
}

func TestHandler_400_InvalidCurrency(t *testing.T) {
	t.Parallel()
	h := newTestHandler(&fakeNetworkClient{})
	body := `{"groups":[{"currency":"eur","payment_method":"SEPA","expiration_seconds":30,"bands":[{"client_quote_id":"c1","max_amount_usd":"1000","rate":"0.86"}]}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_currency") {
		t.Errorf("body: got %q", rec.Body.String())
	}
}

func TestHandler_400_InvalidPaymentMethod(t *testing.T) {
	t.Parallel()
	h := newTestHandler(&fakeNetworkClient{})
	body := `{"groups":[{"currency":"EUR","payment_method":"UNSPECIFIED","expiration_seconds":30,"bands":[{"client_quote_id":"c1","max_amount_usd":"1000","rate":"0.86"}]}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_payment_method") {
		t.Errorf("body: got %q", rec.Body.String())
	}
}

func TestHandler_400_InvalidRate(t *testing.T) {
	t.Parallel()
	h := newTestHandler(&fakeNetworkClient{})
	body := `{"groups":[{"currency":"EUR","payment_method":"SEPA","expiration_seconds":30,"bands":[{"client_quote_id":"c1","max_amount_usd":"1000","rate":"0"}]}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_rate") {
		t.Errorf("body: got %q", rec.Body.String())
	}
}

func TestHandler_401_MissingAuth(t *testing.T) {
	t.Parallel()
	h := newTestHandler(&fakeNetworkClient{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(validBody()))
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

func TestHandler_401_WrongKey(t *testing.T) {
	t.Parallel()
	h := newTestHandler(&fakeNetworkClient{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(validBody()))
	req.Header.Set("Authorization", "Bearer wrong")
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

func TestHandler_409_IdempotencyConflict(t *testing.T) {
	t.Parallel()
	h := newTestHandler(&fakeNetworkClient{})
	// First call: 200 (caches under idem key)
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(validBody()))
	req1.Header.Set("Authorization", "Bearer key1")
	req1.Header.Set("Idempotency-Key", "fixed-key")
	h.ServeHTTP(rec1, req1)
	if rec1.Code != 200 {
		t.Fatalf("first call: got %d, want 200", rec1.Code)
	}
	// Second call with same key, different body
	differentBody := `{"groups":[{"currency":"GBP","payment_method":"SWIFT","expiration_seconds":30,"bands":[{"client_quote_id":"x1","max_amount_usd":"5000","rate":"0.79"}]}]}`
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(differentBody))
	req2.Header.Set("Authorization", "Bearer key1")
	req2.Header.Set("Idempotency-Key", "fixed-key")
	h.ServeHTTP(rec2, req2)
	if rec2.Code != 409 {
		t.Errorf("second call: got %d, want 409", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "idempotency_conflict") {
		t.Errorf("body: got %q", rec2.Body.String())
	}
}

func TestHandler_IdempotencyReplay(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkClient{}
	h := newTestHandler(fake)
	// First call
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(validBody()))
	req1.Header.Set("Authorization", "Bearer key1")
	req1.Header.Set("Idempotency-Key", "replay-key")
	h.ServeHTTP(rec1, req1)
	if rec1.Code != 200 {
		t.Fatalf("first call: got %d, want 200", rec1.Code)
	}
	// Second identical call
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(validBody()))
	req2.Header.Set("Authorization", "Bearer key1")
	req2.Header.Set("Idempotency-Key", "replay-key")
	h.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Errorf("replay: got %d, want 200", rec2.Code)
	}
	if rec2.Header().Get("X-Idempotent-Replay") != "true" {
		t.Errorf("replay header: got %q, want true", rec2.Header().Get("X-Idempotent-Replay"))
	}
	if fake.calls.Load() != 1 {
		t.Errorf("upstream should be called once, got %d", fake.calls.Load())
	}
	if rec1.Body.String() != rec2.Body.String() {
		t.Errorf("replay body should match original\norig: %s\nreplay: %s", rec1.Body.String(), rec2.Body.String())
	}
}

func TestHandler_502_UpstreamUnavailable(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkClient{
		updateQuoteFn: func(ctx context.Context, req *connect.Request[payment.UpdateQuoteRequest]) (*connect.Response[payment.UpdateQuoteResponse], error) {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("network down"))
		},
	}
	h := newTestHandler(fake)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(validBody()))
	req.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec, req)
	if rec.Code != 502 {
		t.Errorf("status: got %d, want 502", rec.Code)
	}
}

func TestHandler_504_UpstreamTimeout(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkClient{
		updateQuoteFn: func(ctx context.Context, req *connect.Request[payment.UpdateQuoteRequest]) (*connect.Response[payment.UpdateQuoteResponse], error) {
			return nil, context.DeadlineExceeded
		},
	}
	h := newTestHandler(fake, func(d *Deps) { d.UpstreamTimeout = 50 * time.Millisecond })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(validBody()))
	req.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec, req)
	if rec.Code != 504 {
		t.Errorf("status: got %d, want 504", rec.Code)
	}
}

func TestHandler_422_UnsupportedBandFromUpstream(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkClient{
		updateQuoteFn: func(ctx context.Context, req *connect.Request[payment.UpdateQuoteRequest]) (*connect.Response[payment.UpdateQuoteResponse], error) {
			return nil, connect.NewError(connect.CodeUnknown, errors.New("unsupported band: max_amount=2000"))
		},
	}
	h := newTestHandler(fake)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(validBody()))
	req.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec, req)
	if rec.Code != 422 {
		t.Errorf("status: got %d, want 422", rec.Code)
	}
}

func TestHandler_429_RateLimited(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkClient{}
	h := newTestHandler(fake, func(d *Deps) {
		d.RequestsPerSec = 1
		d.Burst = 1
	})
	// First call: 200
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(validBody()))
	req1.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec1, req1)
	if rec1.Code != 200 {
		t.Fatalf("first: got %d, want 200", rec1.Code)
	}
	// Second call immediately: 429 (burst exhausted)
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(validBody()))
	req2.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec2, req2)
	if rec2.Code != 429 {
		t.Errorf("second: got %d, want 429", rec2.Code)
	}
}

func TestHandler_413_BodyTooLarge(t *testing.T) {
	t.Parallel()
	h := newTestHandler(&fakeNetworkClient{}, func(d *Deps) { d.MaxBodyBytes = 100 })
	rec := httptest.NewRecorder()
	// 200 bytes body, content-length set
	body := bytes.Repeat([]byte("a"), 200)
	req := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", bytes.NewReader(body))
	req.ContentLength = 200
	req.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec, req)
	if rec.Code != 413 {
		t.Errorf("status: got %d, want 413", rec.Code)
	}
}

func TestHandler_RequestIDHeaderEchoed(t *testing.T) {
	t.Parallel()
	h := newTestHandler(&fakeNetworkClient{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(validBody()))
	req.Header.Set("Authorization", "Bearer key1")
	req.Header.Set("X-Request-Id", "client-id-123")
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-Id"); got != "client-id-123" {
		t.Errorf("X-Request-Id: got %q, want client-id-123", got)
	}
}

func TestHandler_RequestIDGeneratedWhenAbsent(t *testing.T) {
	t.Parallel()
	h := newTestHandler(&fakeNetworkClient{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(validBody()))
	req.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-Id"); got == "" {
		t.Error("X-Request-Id should be set")
	}
}

func TestHandler_MultipleGroupsPassed(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkClient{}
	h := newTestHandler(fake)
	body := `{
		"groups": [
			{"currency":"EUR","payment_method":"SEPA","expiration_seconds":30,
				"bands":[{"client_quote_id":"c1","max_amount_usd":"1000","rate":"0.86"}]},
			{"currency":"GBP","payment_method":"SWIFT","expiration_seconds":60,
				"bands":[{"client_quote_id":"c2","max_amount_usd":"5000","rate":"0.79"}]}
		]
	}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status: got %d, body: %s", rec.Code, rec.Body.String())
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.last.PayOut) != 2 {
		t.Errorf("expected 2 PayOut groups, got %d", len(fake.last.PayOut))
	}
}

func TestHandler_AutoIdempotencyKeyFromBody(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkClient{}
	h := newTestHandler(fake)
	// No Idempotency-Key header — server hashes body
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(validBody()))
	req1.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec1, req1)
	if rec1.Code != 200 {
		t.Fatalf("first: got %d, want 200", rec1.Code)
	}
	// Same body, no header — should replay
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(validBody()))
	req2.Header.Set("Authorization", "Bearer key1")
	h.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Errorf("replay: got %d, want 200", rec2.Code)
	}
	if rec2.Header().Get("X-Idempotent-Replay") != "true" {
		t.Error("expected replay header")
	}
	if fake.calls.Load() != 1 {
		t.Errorf("upstream should be called once, got %d", fake.calls.Load())
	}
}

// Sanity: handler should not crash on concurrent requests sharing the
// same router.
func TestHandler_ConcurrentRequests(t *testing.T) {
	t.Parallel()
	fake := &fakeNetworkClient{}
	h := newTestHandler(fake, func(d *Deps) {
		d.RequestsPerSec = 1000
		d.Burst = 1000
	})
	const N = 20
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/v1/quotes/pay-out", strings.NewReader(validBody()))
			req.Header.Set("Authorization", "Bearer key1")
			req.Header.Set("Idempotency-Key", fmt.Sprintf("key-%d", i))
			h.ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Errorf("goroutine %d: status %d, body: %s", i, rec.Code, rec.Body.String())
			}
		}(i)
	}
	wg.Wait()
	if got := fake.calls.Load(); got != int64(N) {
		t.Errorf("expected %d upstream calls, got %d", N, got)
	}
}
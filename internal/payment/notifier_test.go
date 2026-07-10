package payment

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNoOpNotifier(t *testing.T) {
	n := NewNoOpNotifier()
	p := Payment{ID: 1, PaymentClientID: "c1", Role: RoleProvider, PayoutCurrency: "GBP"}
	ctx := context.Background()

	if err := n.ManualAmlCheckRequired(ctx, p); err != nil {
		t.Errorf("ManualAmlCheckRequired error = %v", err)
	}
	if err := n.AmlApproved(ctx, p); err != nil {
		t.Errorf("AmlApproved error = %v", err)
	}
	if err := n.AmlRejected(ctx, p, "reason"); err != nil {
		t.Errorf("AmlRejected error = %v", err)
	}
	if err := n.QuoteConfirmed(ctx, p); err != nil {
		t.Errorf("QuoteConfirmed error = %v", err)
	}
	if err := n.QuoteRejected(ctx, p, "reason"); err != nil {
		t.Errorf("QuoteRejected error = %v", err)
	}
}

func TestWebhookNotifier_Events(t *testing.T) {
	var received atomic.Int32
	var body atomic.Value
	var sigHeader atomic.Value

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		b, _ := io.ReadAll(r.Body)
		body.Store(string(b))
		sigHeader.Store(r.Header.Get("X-T0-Signature"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	n := NewAMLWebhookNotifier(WebhookConfig{
		URL:        server.URL,
		Secret:     "shhh",
		Timeout:    5 * time.Second,
		MaxRetries: 1,
	})
	p := Payment{
		ID:              7,
		PaymentClientID: "client-7",
		Role:            RoleProvider,
		PayoutCurrency:  "GBP",
		PayoutAmount:    &Decimal{Unscaled: 1000, Exponent: 0},
	}

	tests := []struct {
		name   string
		call   func(context.Context, Payment) error
		event  string
		reason string
	}{
		{
			name:  "manual_aml_check_required",
			call:  func(ctx context.Context, p Payment) error { return n.ManualAmlCheckRequired(ctx, p) },
			event: "manual_aml_check_required",
		},
		{
			name:  "aml_approved",
			call:  func(ctx context.Context, p Payment) error { return n.AmlApproved(ctx, p) },
			event: "aml_approved",
		},
		{
			name:  "aml_rejected",
			call:  func(ctx context.Context, p Payment) error { return n.AmlRejected(ctx, p, "suspicious") },
			event: "aml_rejected",
			reason: "suspicious",
		},
		{
			name:  "quote_confirmed",
			call:  func(ctx context.Context, p Payment) error { return n.QuoteConfirmed(ctx, p) },
			event: "quote_confirmed",
		},
		{
			name:  "quote_rejected",
			call:  func(ctx context.Context, p Payment) error { return n.QuoteRejected(ctx, p, "tolerance") },
			event: "quote_rejected",
			reason: "tolerance",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			received.Store(int32(0))
			body.Store("")
			sigHeader.Store("")

			if err := tt.call(context.Background(), p); err != nil {
				t.Fatalf("call error = %v", err)
			}
			if received.Load() != 1 {
				t.Fatalf("expected 1 request, got %d", received.Load())
			}

			var payload amlEventPayload
			if err := json.Unmarshal([]byte(body.Load().(string)), &payload); err != nil {
				t.Fatalf("unmarshal payload: %v", err)
			}
			if payload.Event != tt.event {
				t.Errorf("expected event %s, got %s", tt.event, payload.Event)
			}
			if payload.PaymentID != p.ID {
				t.Errorf("expected payment id %d, got %d", p.ID, payload.PaymentID)
			}
			if payload.Role != p.Role {
				t.Errorf("expected role %s, got %s", p.Role, payload.Role)
			}
			if payload.Reason != tt.reason {
				t.Errorf("expected reason %q, got %q", tt.reason, payload.Reason)
			}

			expectedSig := expectedHMAC([]byte(body.Load().(string)), "shhh")
			if sigHeader.Load().(string) != expectedSig {
				t.Errorf("expected signature %s, got %s", expectedSig, sigHeader.Load())
			}
		})
	}
}

func TestWebhookNotifier_NoSecret(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-T0-Signature") != "" {
			t.Error("expected no signature header")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	n := NewAMLWebhookNotifier(WebhookConfig{URL: server.URL, MaxRetries: 0})
	if err := n.ManualAmlCheckRequired(context.Background(), Payment{}); err != nil {
		t.Fatalf("error = %v", err)
	}
}

func TestWebhookNotifier_RetryThenSuccess(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	n := NewAMLWebhookNotifier(WebhookConfig{
		URL:        server.URL,
		MaxRetries: 5,
	})
	start := time.Now()
	if err := n.AmlApproved(context.Background(), Payment{ID: 1}); err != nil {
		t.Fatalf("error = %v", err)
	}
	elapsed := time.Since(start)
	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts.Load())
	}
	if elapsed < 2*time.Second || elapsed > 5*time.Second {
		t.Errorf("expected ~2s backoff (1s+2s), got %v", elapsed)
	}
}

func TestWebhookNotifier_RetryExhausted(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	n := NewAMLWebhookNotifier(WebhookConfig{
		URL:        server.URL,
		MaxRetries: 2,
	})
	if err := n.AmlRejected(context.Background(), Payment{ID: 1}, "bad"); err == nil {
		t.Fatal("expected error")
	}
	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts (initial + 2 retries), got %d", attempts.Load())
	}
}

func TestWebhookNotifier_ContextCancelled(t *testing.T) {
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	n := NewAMLWebhookNotifier(WebhookConfig{
		URL:        server.URL,
		Timeout:    5 * time.Second,
		MaxRetries: 1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := n.QuoteConfirmed(ctx, Payment{ID: 1})
	close(block)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestWebhookNotifier_Defaults(t *testing.T) {
	n := NewAMLWebhookNotifier(WebhookConfig{})
	if n.cfg.Timeout != 10*time.Second {
		t.Errorf("expected default timeout 10s, got %v", n.cfg.Timeout)
	}
	if n.cfg.MaxRetries != 5 {
		t.Errorf("expected default max retries 5, got %d", n.cfg.MaxRetries)
	}
}

func expectedHMAC(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestWebhookBackoff(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	n := NewAMLWebhookNotifier(WebhookConfig{
		URL:        server.URL,
		MaxRetries: 4,
	})
	start := time.Now()
	err := n.AmlApproved(context.Background(), Payment{ID: 1})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts.Load() != 5 {
		t.Errorf("expected 5 attempts, got %d", attempts.Load())
	}
	// 1+2+4+8 = 15s, but capped at 30s per backoff.
	if elapsed < 14*time.Second || elapsed > 20*time.Second {
		t.Errorf("expected ~15s backoff, got %v", elapsed)
	}
	if !strings.Contains(err.Error(), "webhook delivery failed after 4 retries") {
		t.Errorf("expected retry exhausted message, got %v", err)
	}
}

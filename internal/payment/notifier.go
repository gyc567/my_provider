package payment

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// AMLNotifier sends lifecycle events for the manual AML workflow.
type AMLNotifier interface {
	ManualAmlCheckRequired(ctx context.Context, p Payment) error
	AmlApproved(ctx context.Context, p Payment) error
	AmlRejected(ctx context.Context, p Payment, reason string) error
	QuoteConfirmed(ctx context.Context, p Payment) error
	QuoteRejected(ctx context.Context, p Payment, reason string) error
}

// WebhookConfig configures webhook delivery for AML events.
type WebhookConfig struct {
	URL        string
	Secret     string
	Timeout    time.Duration
	MaxRetries int
}

// AMLWebhookNotifier delivers AML events to an HTTP endpoint with HMAC-SHA256 signing
// and exponential backoff retry.
type AMLWebhookNotifier struct {
	cfg    WebhookConfig
	client *http.Client
}

// NewAMLWebhookNotifier creates a notifier that POSTs JSON events to cfg.URL.
// If cfg.Secret is non-empty, an X-T0-Signature header with HMAC-SHA256(hex) is included.
// MaxRetries defaults to 5 when zero; Timeout defaults to 10s when zero.
func NewAMLWebhookNotifier(cfg WebhookConfig) *AMLWebhookNotifier {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 5
	}
	return &AMLWebhookNotifier{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// NoOpNotifier discards all AML events.
type NoOpNotifier struct{}

// NewNoOpNotifier returns a notifier that does nothing.
func NewNoOpNotifier() *NoOpNotifier { return &NoOpNotifier{} }

// ManualAmlCheckRequired implements AMLNotifier.
func (NoOpNotifier) ManualAmlCheckRequired(context.Context, Payment) error { return nil }

// AmlApproved implements AMLNotifier.
func (NoOpNotifier) AmlApproved(context.Context, Payment) error { return nil }

// AmlRejected implements AMLNotifier.
func (NoOpNotifier) AmlRejected(context.Context, Payment, string) error { return nil }

// QuoteConfirmed implements AMLNotifier.
func (NoOpNotifier) QuoteConfirmed(context.Context, Payment) error { return nil }

// QuoteRejected implements AMLNotifier.
func (NoOpNotifier) QuoteRejected(context.Context, Payment, string) error { return nil }

// ManualAmlCheckRequired implements AMLNotifier.
func (n *AMLWebhookNotifier) ManualAmlCheckRequired(ctx context.Context, p Payment) error {
	return n.send(ctx, amlEvent("manual_aml_check_required", p, ""))
}

// AmlApproved implements AMLNotifier.
func (n *AMLWebhookNotifier) AmlApproved(ctx context.Context, p Payment) error {
	return n.send(ctx, amlEvent("aml_approved", p, ""))
}

// AmlRejected implements AMLNotifier.
func (n *AMLWebhookNotifier) AmlRejected(ctx context.Context, p Payment, reason string) error {
	return n.send(ctx, amlEvent("aml_rejected", p, reason))
}

// QuoteConfirmed implements AMLNotifier.
func (n *AMLWebhookNotifier) QuoteConfirmed(ctx context.Context, p Payment) error {
	return n.send(ctx, amlEvent("quote_confirmed", p, ""))
}

// QuoteRejected implements AMLNotifier.
func (n *AMLWebhookNotifier) QuoteRejected(ctx context.Context, p Payment, reason string) error {
	return n.send(ctx, amlEvent("quote_rejected", p, reason))
}

type amlEventPayload struct {
	Event           string     `json:"event"`
	PaymentID       int64      `json:"payment_id"`
	PaymentClientID string     `json:"payment_client_id"`
	Role            Role       `json:"role"`
	Currency        string     `json:"currency,omitempty"`
	Amount          *Decimal   `json:"amount,omitempty"`
	Reason          string     `json:"reason,omitempty"`
	Timestamp       time.Time  `json:"timestamp"`
}

func amlEvent(event string, p Payment, reason string) amlEventPayload {
	return amlEventPayload{
		Event:           event,
		PaymentID:       p.ID,
		PaymentClientID: p.PaymentClientID,
		Role:            p.Role,
		Currency:        p.PayoutCurrency,
		Amount:          p.PayoutAmount,
		Reason:          reason,
		Timestamp:       time.Now().UTC(),
	}
}

func (n *AMLWebhookNotifier) send(ctx context.Context, event amlEventPayload) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling aml event: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= n.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		lastErr = n.post(ctx, body)
		if lastErr == nil {
			return nil
		}
	}
	return fmt.Errorf("webhook delivery failed after %d retries: %w", n.cfg.MaxRetries, lastErr)
}

func (n *AMLWebhookNotifier) post(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "t0-provider/1.0")

	if n.cfg.Secret != "" {
		req.Header.Set("X-T0-Signature", hmacSignature(body, n.cfg.Secret))
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("sending webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %s", resp.Status)
	}
	return nil
}

func hmacSignature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

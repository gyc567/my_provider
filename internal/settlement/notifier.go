package settlement

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

// Event is a normalized settlement or ledger notification sent to a downstream webhook.
type Event struct {
	Type          string        `json:"type"`
	CounterpartID uint64        `json:"counterpartId,omitempty"`
	CreditLimit   *Decimal      `json:"creditLimit,omitempty"`
	PayoutLimit   *Decimal      `json:"payoutLimit,omitempty"`
	CreditUsage   *Decimal      `json:"creditUsage,omitempty"`
	Reserve       *Decimal      `json:"reserve,omitempty"`
	LedgerEntries []LedgerEntry `json:"ledgerEntries,omitempty"`
}

// Notifier sends settlement events to an external consumer.
type Notifier interface {
	Notify(ctx context.Context, event Event) error
}

// WebhookNotifier delivers events to a configured HTTP endpoint with optional HMAC-SHA256 signing.
type WebhookNotifier struct {
	url    string
	secret string
	client *http.Client
}

// NewWebhookNotifier creates a notifier that POSTs JSON events to url.
// If secret is non-empty, an X-T0-Signature header with HMAC-SHA256(hex) is included.
func NewWebhookNotifier(url, secret string) *WebhookNotifier {
	return &WebhookNotifier{
		url:    url,
		secret: secret,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Notify implements Notifier.
func (n *WebhookNotifier) Notify(ctx context.Context, event Event) error {
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling event: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "t0-provider/1.0")

	if n.secret != "" {
		req.Header.Set("X-T0-Signature", signature(body, n.secret))
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

func signature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// NoOpNotifier discards all events. It is used when no webhook is configured.
type NoOpNotifier struct{}

// NewNoOpNotifier returns a notifier that does nothing.
func NewNoOpNotifier() *NoOpNotifier { return &NoOpNotifier{} }

// Notify implements Notifier.
func (NoOpNotifier) Notify(_ context.Context, _ Event) error { return nil }

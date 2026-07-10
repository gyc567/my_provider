package settlement

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebhookNotifier_Notify(t *testing.T) {
	var received []byte
	var sig string
	secret := "shhh"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		sig = r.Header.Get("X-T0-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	n := NewWebhookNotifier(server.URL, secret)
	event := Event{Type: "settlement.credit", CounterpartID: 2}
	if err := n.Notify(context.Background(), event); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}

	if len(received) == 0 {
		t.Fatal("expected webhook payload")
	}
	var decoded Event
	if err := json.Unmarshal(received, &decoded); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if decoded.CounterpartID != 2 {
		t.Errorf("expected counterpart 2, got %d", decoded.CounterpartID)
	}

	expected := signature(received, secret)
	if sig != expected {
		t.Errorf("expected signature %s, got %s", expected, sig)
	}
}

func TestWebhookNotifier_NoSecret(t *testing.T) {
	var sig string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sig = r.Header.Get("X-T0-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	n := NewWebhookNotifier(server.URL, "")
	if err := n.Notify(context.Background(), Event{Type: "settlement.ledger"}); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if sig != "" {
		t.Errorf("expected no signature, got %s", sig)
	}
}

func TestWebhookNotifier_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	n := NewWebhookNotifier(server.URL, "")
	if err := n.Notify(context.Background(), Event{}); err == nil {
		t.Fatal("expected error for 5xx response")
	}
}

func TestSignature(t *testing.T) {
	body := []byte(`{"type":"settlement.credit"}`)
	secret := "secret"
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	if got := signature(body, secret); got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

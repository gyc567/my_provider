package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	sdkprovider "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/provider"
	"my-provider/internal/paymentintent"
)

type fakeProviderNetwork struct {
	confirmed []uint64
	rejected  []uint64
}

func (f *fakeProviderNetwork) ConfirmPayment(_ context.Context, req *connect.Request[sdkprovider.ConfirmPaymentRequest]) (*connect.Response[sdkprovider.ConfirmPaymentResponse], error) {
	f.confirmed = append(f.confirmed, req.Msg.PaymentIntentId)
	return connect.NewResponse(&sdkprovider.ConfirmPaymentResponse{
		PayoutProviderId: 42,
		SettlementAmount: &common.Decimal{Unscaled: 100, Exponent: 0},
	}), nil
}

func (f *fakeProviderNetwork) RejectPaymentIntent(_ context.Context, req *connect.Request[sdkprovider.RejectPaymentIntentRequest]) (*connect.Response[sdkprovider.RejectPaymentIntentResponse], error) {
	f.rejected = append(f.rejected, req.Msg.PaymentIntentId)
	return connect.NewResponse(&sdkprovider.RejectPaymentIntentResponse{}), nil
}

func (f *fakeProviderNetwork) ConfirmSettlement(_ context.Context, _ *connect.Request[sdkprovider.ConfirmSettlementRequest]) (*connect.Response[sdkprovider.ConfirmSettlementResponse], error) {
	return connect.NewResponse(&sdkprovider.ConfirmSettlementResponse{}), nil
}

var _ NetworkClient = (*fakeProviderNetwork)(nil)

func newTestProviderAPIHandler(t *testing.T) (*APIHandler, *fakeProviderNetwork, paymentintent.Store) {
	t.Helper()
	store, err := paymentintent.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	net := &fakeProviderNetwork{}
	return NewAPIHandler(store, net, []string{"test-key"}), net, store
}

func seedProviderIntent(ctx context.Context, t *testing.T, store paymentintent.Store, id uint64) {
	t.Helper()
	_, _, err := store.GetOrCreate(ctx, paymentintent.PaymentIntent{
		ID:       id,
		Role:     paymentintent.RolePayInProvider,
		Currency: "EUR",
		Amount:   &paymentintent.Decimal{Unscaled: 100, Exponent: 0},
		Status:   paymentintent.StatusCreated,
	})
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
}

func TestProviderAPI_Get(t *testing.T) {
	h, _, store := newTestProviderAPIHandler(t)
	ctx := context.Background()
	seedProviderIntent(ctx, t, store, 1)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/payment-intents/provider/1", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	http.StripPrefix("/api/v1/payment-intents/provider", h.Router()).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp PaymentIntentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ID != 1 {
		t.Errorf("expected id 1, got %d", resp.ID)
	}
}

func TestProviderAPI_Confirm(t *testing.T) {
	h, net, store := newTestProviderAPIHandler(t)
	ctx := context.Background()
	seedProviderIntent(ctx, t, store, 2)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payment-intents/provider/2/confirm", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	http.StripPrefix("/api/v1/payment-intents/provider", h.Router()).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(net.confirmed) != 1 || net.confirmed[0] != 2 {
		t.Errorf("expected confirm for 2, got %v", net.confirmed)
	}
	pi, err := store.Get(ctx, 2, paymentintent.RolePayInProvider)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if pi.Status != paymentintent.StatusFundsReceived {
		t.Errorf("expected status %s, got %s", paymentintent.StatusFundsReceived, pi.Status)
	}
}

func TestProviderAPI_Reject(t *testing.T) {
	h, net, store := newTestProviderAPIHandler(t)
	ctx := context.Background()
	seedProviderIntent(ctx, t, store, 3)

	body := `{"reason": "failed kyc"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payment-intents/provider/3/reject", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	http.StripPrefix("/api/v1/payment-intents/provider", h.Router()).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(net.rejected) != 1 || net.rejected[0] != 3 {
		t.Errorf("expected reject for 3, got %v", net.rejected)
	}
	pi, err := store.Get(ctx, 3, paymentintent.RolePayInProvider)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if pi.Status != paymentintent.StatusRejected {
		t.Errorf("expected status %s, got %s", paymentintent.StatusRejected, pi.Status)
	}
}

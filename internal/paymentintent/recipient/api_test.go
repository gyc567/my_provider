package recipient

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"connectrpc.com/connect"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	sdkrecipient "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/recipient"
	"my-provider/internal/paymentintent"
)

type fakeRecipientNetwork struct {
	createdID uint64
}

func (f *fakeRecipientNetwork) CreatePaymentIntent(_ context.Context, req *connect.Request[sdkrecipient.CreatePaymentIntentRequest]) (*connect.Response[sdkrecipient.CreatePaymentIntentResponse], error) {
	return connect.NewResponse(&sdkrecipient.CreatePaymentIntentResponse{
		PaymentIntentId: f.createdID,
		PayInPaymentMethods: []*sdkrecipient.CreatePaymentIntentResponse_PaymentMethod{
			{
				PaymentMethod: common.PaymentMethodType_PAYMENT_METHOD_TYPE_SEPA,
				PaymentUrl:    "https://example.com/pay/" + formatUint(f.createdID),
			},
		},
	}), nil
}

func (f *fakeRecipientNetwork) GetQuote(_ context.Context, _ *connect.Request[sdkrecipient.GetQuoteRequest]) (*connect.Response[sdkrecipient.GetQuoteResponse], error) {
	return connect.NewResponse(&sdkrecipient.GetQuoteResponse{
		Result: &sdkrecipient.GetQuoteResponse_Quote_{
			Quote: &sdkrecipient.GetQuoteResponse_Quote{
				Rate: &common.Decimal{Unscaled: 85, Exponent: -2},
			},
		},
	}), nil
}

var _ NetworkClient = (*fakeRecipientNetwork)(nil)

func newTestRecipientAPIHandler(t *testing.T) (*APIHandler, *fakeRecipientNetwork, paymentintent.Store) {
	t.Helper()
	store, err := paymentintent.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	net := &fakeRecipientNetwork{createdID: 555}
	return NewAPIHandler(store, net, []string{"test-key"}), net, store
}

func TestRecipientAPI_GetQuote(t *testing.T) {
	h, _, _ := newTestRecipientAPIHandler(t)

	body := `{
		"payInCurrency": "EUR",
		"payInAmount": {"unscaled": 100, "exponent": 0},
		"payOutCurrency": "GBP",
		"payInPaymentMethod": "PAYMENT_METHOD_TYPE_SEPA",
		"payOutPaymentMethod": "PAYMENT_METHOD_TYPE_SWIFT"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payment-intent-quotes", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	http.StripPrefix("/api/v1", h.Router()).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"Quote"`)) {
		t.Errorf("expected response to contain quote, got %s", w.Body.String())
	}
}

func TestRecipientAPI_CreatePaymentIntent(t *testing.T) {
	h, _, store := newTestRecipientAPIHandler(t)

	body := `{
		"paymentReference": "ref-1",
		"payInCurrency": "EUR",
		"payInAmount": {"unscaled": 200, "exponent": 0},
		"payOutCurrency": "GBP",
		"payOutDetails": {"sepa": {"iban": "IBAN", "beneficiaryName": "Bob"}}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payment-intents", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	http.StripPrefix("/api/v1", h.Router()).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp sdkrecipient.CreatePaymentIntentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.PaymentIntentId != 555 {
		t.Errorf("expected intent id 555, got %d", resp.PaymentIntentId)
	}

	pi, err := store.Get(context.Background(), 555, paymentintent.RoleBeneficiary)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if pi.Role != paymentintent.RoleBeneficiary {
		t.Errorf("expected role %s, got %s", paymentintent.RoleBeneficiary, pi.Role)
	}
}

func TestRecipientAPI_Unauthorized(t *testing.T) {
	h, _, _ := newTestRecipientAPIHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payment-intents", nil)
	w := httptest.NewRecorder()
	http.StripPrefix("/api/v1", h.Router()).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func formatUint(u uint64) string {
	var buf [32]byte
	return string(strconv.AppendUint(buf[:0], u, 10))
}

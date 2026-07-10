// Package e2e exercises the full HTTP surface of the provider proxy with in-memory stores and stubbed network clients.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	httpSwagger "github.com/swaggo/http-swagger"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
	sdkprovider "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/provider"
	paymentintentproviderconnect "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/provider/providerconnect"
	sdkrecipient "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/recipient"
	paymentintentrecipientconnect "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/recipient/recipientconnect"
	"github.com/t-0-network/provider-sdk-go/provider"
	_ "my-provider/docs"
	"my-provider/internal/api"
	"my-provider/internal/handler"
	localpayment "my-provider/internal/payment"
	"my-provider/internal/paymentintent"
	paymentintentprovider "my-provider/internal/paymentintent/provider"
	paymentintentrecipient "my-provider/internal/paymentintent/recipient"
	"my-provider/internal/quote"
	"my-provider/internal/quoteapi"
	"my-provider/internal/settlement"
)

const testAPIKey = "test-key"

// ---------- stub network clients ----------

type fakePaymentNetwork struct {
	quoteCounter    int
	createdPayments []uint64
}

func (f *fakePaymentNetwork) UpdateQuote(_ context.Context, _ *connect.Request[payment.UpdateQuoteRequest]) (*connect.Response[payment.UpdateQuoteResponse], error) {
	f.quoteCounter++
	return connect.NewResponse(&payment.UpdateQuoteResponse{}), nil
}

func (f *fakePaymentNetwork) GetQuote(_ context.Context, _ *connect.Request[payment.GetQuoteRequest]) (*connect.Response[payment.GetQuoteResponse], error) {
	return connect.NewResponse(&payment.GetQuoteResponse{
		Result: &payment.GetQuoteResponse_Success_{
			Success: &payment.GetQuoteResponse_Success{
				QuoteId: &payment.QuoteId{QuoteId: 42, ProviderId: 7},
			},
		},
	}), nil
}

func (f *fakePaymentNetwork) CreatePayment(_ context.Context, _ *connect.Request[payment.CreatePaymentRequest]) (*connect.Response[payment.CreatePaymentResponse], error) {
	id := uint64(1000 + len(f.createdPayments))
	f.createdPayments = append(f.createdPayments, id)
	return connect.NewResponse(&payment.CreatePaymentResponse{
		Result: &payment.CreatePaymentResponse_Accepted_{
			Accepted: &payment.CreatePaymentResponse_Accepted{
				PaymentId:        id,
				PayoutProviderId: 7,
				PayoutAmount:     &common.Decimal{Unscaled: 950, Exponent: 0},
			},
		},
	}), nil
}

//nolint:staticcheck // Required to satisfy paymentconnect.NetworkServiceClient interface.
func (f *fakePaymentNetwork) ConfirmPayout(_ context.Context, _ *connect.Request[payment.ConfirmPayoutRequest]) (*connect.Response[payment.ConfirmPayoutResponse], error) {
	return connect.NewResponse(&payment.ConfirmPayoutResponse{}), nil
}

func (f *fakePaymentNetwork) FinalizePayout(_ context.Context, _ *connect.Request[payment.FinalizePayoutRequest]) (*connect.Response[payment.FinalizePayoutResponse], error) {
	return connect.NewResponse(&payment.FinalizePayoutResponse{}), nil
}

func (f *fakePaymentNetwork) CompleteManualAmlCheck(_ context.Context, _ *connect.Request[payment.CompleteManualAmlCheckRequest]) (*connect.Response[payment.CompleteManualAmlCheckResponse], error) {
	return connect.NewResponse(&payment.CompleteManualAmlCheckResponse{}), nil
}

var _ paymentconnect.NetworkServiceClient = (*fakePaymentNetwork)(nil)

type fakeProviderIntentNetwork struct{}

func (f *fakeProviderIntentNetwork) ConfirmPayment(_ context.Context, _ *connect.Request[sdkprovider.ConfirmPaymentRequest]) (*connect.Response[sdkprovider.ConfirmPaymentResponse], error) {
	return connect.NewResponse(&sdkprovider.ConfirmPaymentResponse{
		PayoutProviderId: 42,
		SettlementAmount: &common.Decimal{Unscaled: 100, Exponent: 0},
	}), nil
}

func (f *fakeProviderIntentNetwork) RejectPaymentIntent(_ context.Context, _ *connect.Request[sdkprovider.RejectPaymentIntentRequest]) (*connect.Response[sdkprovider.RejectPaymentIntentResponse], error) {
	return connect.NewResponse(&sdkprovider.RejectPaymentIntentResponse{}), nil
}

func (f *fakeProviderIntentNetwork) ConfirmSettlement(_ context.Context, _ *connect.Request[sdkprovider.ConfirmSettlementRequest]) (*connect.Response[sdkprovider.ConfirmSettlementResponse], error) {
	return connect.NewResponse(&sdkprovider.ConfirmSettlementResponse{}), nil
}

var _ paymentintentproviderconnect.NetworkServiceClient = (*fakeProviderIntentNetwork)(nil)

type fakeRecipientIntentNetwork struct {
	nextID uint64
}

func (f *fakeRecipientIntentNetwork) CreatePaymentIntent(_ context.Context, _ *connect.Request[sdkrecipient.CreatePaymentIntentRequest]) (*connect.Response[sdkrecipient.CreatePaymentIntentResponse], error) {
	f.nextID++
	return connect.NewResponse(&sdkrecipient.CreatePaymentIntentResponse{
		PaymentIntentId: f.nextID,
		PayInPaymentMethods: []*sdkrecipient.CreatePaymentIntentResponse_PaymentMethod{
			{PaymentMethod: common.PaymentMethodType_PAYMENT_METHOD_TYPE_SEPA},
		},
	}), nil
}

func (f *fakeRecipientIntentNetwork) GetQuote(_ context.Context, _ *connect.Request[sdkrecipient.GetQuoteRequest]) (*connect.Response[sdkrecipient.GetQuoteResponse], error) {
	return connect.NewResponse(&sdkrecipient.GetQuoteResponse{
		Result: &sdkrecipient.GetQuoteResponse_Quote_{
			Quote: &sdkrecipient.GetQuoteResponse_Quote{
				Rate: &common.Decimal{Unscaled: 85, Exponent: -2},
			},
		},
	}), nil
}

var _ paymentintentrecipientconnect.NetworkServiceClient = (*fakeRecipientIntentNetwork)(nil)

// ---------- test server setup ----------

type testServer struct {
	baseURL              string
	paymentStore         localpayment.Store
	paymentIntentStore   paymentintent.Store
	quoteStore           quote.Store
	settlementStore      settlement.Store
	fakePaymentNetwork   *fakePaymentNetwork
	fakeRecipientNetwork *fakeRecipientIntentNetwork
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()

	ctx := context.Background()

	dbDir := t.TempDir()
	quoteStore, err := quote.NewSQLiteStore(dbDir + "/quotes.db")
	if err != nil {
		t.Fatalf("quote store: %v", err)
	}
	paymentStore, err := localpayment.NewSQLiteStore(dbDir + "/payments.db")
	if err != nil {
		t.Fatalf("payment store: %v", err)
	}
	settlementStore, err := settlement.NewSQLiteStore(dbDir + "/settlements.db")
	if err != nil {
		t.Fatalf("settlement store: %v", err)
	}
	paymentIntentStore, err := paymentintent.NewSQLiteStore(dbDir + "/intents.db")
	if err != nil {
		t.Fatalf("payment intent store: %v", err)
	}

	t.Cleanup(func() {
		_ = quoteStore.Close()
		_ = paymentStore.Close()
		_ = settlementStore.Close()
		_ = paymentIntentStore.Close()
	})

	fakePaymentNet := &fakePaymentNetwork{}
	fakeProviderNet := &fakeProviderIntentNetwork{}
	fakeRecipientNet := &fakeRecipientIntentNetwork{}

	keys := []string{testAPIKey}

	sdkHandler, err := provider.NewHttpHandler(
		provider.NetworkPublicKeyHexed("0x041b6acf3e830b593aaa992f2f1543dc8063197acfeecefd65135259327ef3166acaca83d62db19eb4fecb3d04e44094378839b8c13a2af26bf78fed56a4af935b"),
		provider.Handler(paymentconnect.NewProviderServiceHandler,
			paymentconnect.ProviderServiceHandler(handler.NewProviderServiceImplementation(fakePaymentNet, paymentStore, settlementStore, settlement.NewNoOpNotifier(), 1.0))),
	)
	if err != nil {
		t.Fatalf("provider sdk handler: %v", err)
	}

	piProviderClient := paymentintentprovider.NewNetworkClient(fakeProviderNet)
	piProviderHandler := paymentintentprovider.NewHandler(paymentIntentStore, piProviderClient, "https://example.com/pay")
	piSdkHandler, err := provider.NewHttpHandler(
		provider.NetworkPublicKeyHexed("0x041b6acf3e830b593aaa992f2f1543dc8063197acfeecefd65135259327ef3166acaca83d62db19eb4fecb3d04e44094378839b8c13a2af26bf78fed56a4af935b"),
		provider.Handler(paymentintentproviderconnect.NewProviderServiceHandler,
			paymentintentproviderconnect.ProviderServiceHandler(piProviderHandler)),
	)
	if err != nil {
		t.Fatalf("payment intent provider sdk handler: %v", err)
	}

	piRecipientClient := paymentintentrecipient.NewNetworkClient(fakeRecipientNet)
	piRecipientHandler := paymentintentrecipient.NewHandler(paymentIntentStore)
	piRecipientSdkHandler, err := provider.NewHttpHandler(
		provider.NetworkPublicKeyHexed("0x041b6acf3e830b593aaa992f2f1543dc8063197acfeecefd65135259327ef3166acaca83d62db19eb4fecb3d04e44094378839b8c13a2af26bf78fed56a4af935b"),
		provider.Handler(paymentintentrecipientconnect.NewRecipientServiceHandler,
			paymentintentrecipientconnect.RecipientServiceHandler(piRecipientHandler)),
	)
	if err != nil {
		t.Fatalf("payment intent recipient sdk handler: %v", err)
	}

	productHandler := api.NewRouter(api.Deps{
		NetworkClient:   fakePaymentNet,
		APIKeys:         keys,
		MaxBodyBytes:    64 << 10,
		RequestsPerSec:  20,
		Burst:           40,
		UpstreamTimeout: 5 * 1e9,
		IdempotencyTTL:  60 * 1e9,
	})

	publisher := quote.NewPublisher(quoteStore, fakePaymentNet, false, false)
	quoteHandler := quoteapi.NewHandler(quoteStore, publisher, fakePaymentNet, keys)
	paymentClient := localpayment.NewNetworkClient(fakePaymentNet)
	paymentHandler := localpayment.NewHandler(paymentStore, paymentClient, keys)
	settlementHandler := settlement.NewAPIHandler(settlementStore, keys)
	piProviderAPI := paymentintentprovider.NewAPIHandler(paymentIntentStore, piProviderClient, keys)
	piRecipientAPI := paymentintentrecipient.NewAPIHandler(paymentIntentStore, piRecipientClient, keys)

	rootMux := http.NewServeMux()
	rootMux.Handle("/tzero.v1.payment.ProviderService/", sdkHandler)
	rootMux.Handle("/tzero.v1.payment_intent.provider.ProviderService/", piSdkHandler)
	rootMux.Handle("/tzero.v1.payment_intent.recipient.RecipientService/", piRecipientSdkHandler)
	rootMux.Handle("/api/v1/quotes/pay-out", productHandler)
	rootMux.Handle("/api/v1/quotes", quoteHandler.Router())
	rootMux.Handle("/api/v1/quotes/", quoteHandler.Router())
	rootMux.Handle("/api/v1/payments", paymentHandler.Router())
	rootMux.Handle("/api/v1/payments/", paymentHandler.Router())
	rootMux.Handle("/api/v1/settlement", settlementHandler.Router())
	rootMux.Handle("/api/v1/settlement/", settlementHandler.Router())
	rootMux.Handle("/api/v1/payment-intents/provider/", http.StripPrefix("/api/v1/payment-intents/provider", piProviderAPI.Router()))
	rootMux.Handle("/api/v1/", http.StripPrefix("/api/v1", piRecipientAPI.Router()))
	rootMux.Handle("/swagger/", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"),
	))

	// Warm up default quote snapshots so GET /api/v1/quotes returns 200.
	_ = publisher.Publish(ctx)

	srv := httptest.NewServer(rootMux)
	t.Cleanup(srv.Close)

	return &testServer{
		baseURL:              srv.URL,
		paymentStore:         paymentStore,
		paymentIntentStore:   paymentIntentStore,
		quoteStore:           quoteStore,
		settlementStore:      settlementStore,
		fakePaymentNetwork:   fakePaymentNet,
		fakeRecipientNetwork: fakeRecipientNet,
	}
}

func (s *testServer) request(t *testing.T, method, path string, body []byte) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, s.baseURL+path, bodyReader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testAPIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

// ---------- e2e test cases ----------

func TestE2E_QuoteLifecycle(t *testing.T) {
	srv := newTestServer(t)

	payOutBody := `{
		"groups": [{
			"currency": "GBP",
			"payment_method": "SWIFT",
			"expiration_seconds": 300,
			"bands": [{
				"client_quote_id": "gbp-1k",
				"max_amount_usd": "1000",
				"rate": "0.79"
			}]
		}]
	}`
	resp := srv.request(t, http.MethodPut, "/api/v1/quotes/pay-out", []byte(payOutBody))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update pay-out: expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	payInBody := `{
		"groups": [{
			"currency": "EUR",
			"paymentMethod": "PAYMENT_METHOD_TYPE_SEPA",
			"expiration": "2099-01-01T00:00:00Z",
			"timestamp": "2024-01-01T00:00:00Z",
			"bands": [{
				"clientQuoteId": "eur-1k",
				"maxAmount": {"unscaled": 1000, "exponent": 0},
				"rate": {"unscaled": 11628, "exponent": -6}
			}]
		}]
	}`
	resp = srv.request(t, http.MethodPut, "/api/v1/quotes/pay-in", []byte(payInBody))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update pay-in: expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	resp = srv.request(t, http.MethodGet, "/api/v1/quotes", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get quotes: expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
	var quotes map[string]any
	if err := json.Unmarshal([]byte(readBody(t, resp)), &quotes); err != nil {
		t.Fatalf("unmarshal quotes: %v", err)
	}
	if _, ok := quotes["payOut"]; !ok {
		t.Error("expected payOut in quotes response")
	}
	if _, ok := quotes["payIn"]; !ok {
		t.Error("expected payIn in quotes response")
	}

	quoteReq := `{
		"amount": {"unscaled": 500, "exponent": 0},
		"amountType": "pay_out",
		"payOutCurrency": "GBP",
		"payOutMethod": "PAYMENT_METHOD_TYPE_SWIFT"
	}`
	resp = srv.request(t, http.MethodPost, "/api/v1/quotes/network", []byte(quoteReq))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("network quote: expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
	body := readBody(t, resp)
	if !bytes.Contains([]byte(body), []byte(`"Success"`)) && !bytes.Contains([]byte(body), []byte(`"success"`)) {
		t.Errorf("expected network quote success, got %s", body)
	}
}

func TestE2E_PaymentLifecycle(t *testing.T) {
	srv := newTestServer(t)

	body := `{
		"paymentClientId": "e2e-client-1",
		"amount": {"unscaled": 1000, "exponent": 0},
		"amountType": "pay_out",
		"currency": "GBP",
		"paymentMethod": "PAYMENT_METHOD_TYPE_SWIFT",
		"paymentDetails": {"accountNumber":"123","swiftCode":"ABC","beneficiaryName":"Bob"}
	}`
	resp := srv.request(t, http.MethodPost, "/api/v1/payments", []byte(body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create payment: expected 201, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(readBody(t, resp)), &created); err != nil {
		t.Fatalf("unmarshal created payment: %v", err)
	}
	id := int64(created["ID"].(float64))
	if created["Role"] != string(localpayment.RoleOFI) {
		t.Errorf("expected role ofi, got %v", created["Role"])
	}

	resp = srv.request(t, http.MethodGet, fmt.Sprintf("/api/v1/payments/%d", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get payment: expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	resp = srv.request(t, http.MethodPost, fmt.Sprintf("/api/v1/payments/%d/aml/approve", id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("aml approve: expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	finalize := `{"success": true, "payoutId": "payout-123", "receipt": "receipt-123"}`
	resp = srv.request(t, http.MethodPost, fmt.Sprintf("/api/v1/payments/%d/finalize", id), []byte(finalize))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("finalize: expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
}

func TestE2E_SettlementREST(t *testing.T) {
	srv := newTestServer(t)

	resp := srv.request(t, http.MethodGet, "/api/v1/settlement/credits", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get credits: expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	resp = srv.request(t, http.MethodGet, "/api/v1/settlement/ledger", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get ledger: expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
}

func TestE2E_PaymentIntentProvider(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()

	// Seed a provider intent locally, then confirm it via REST.
	_, _, err := srv.paymentIntentStore.GetOrCreate(ctx, paymentintent.PaymentIntent{
		ID:       10,
		Role:     paymentintent.RolePayInProvider,
		Currency: "EUR",
		Amount:   &paymentintent.Decimal{Unscaled: 100, Exponent: 0},
		Status:   paymentintent.StatusCreated,
	})
	if err != nil {
		t.Fatalf("seed intent: %v", err)
	}

	resp := srv.request(t, http.MethodPost, "/api/v1/payment-intents/provider/10/confirm", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("provider confirm: expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	pi, err := srv.paymentIntentStore.Get(ctx, 10, paymentintent.RolePayInProvider)
	if err != nil {
		t.Fatalf("get intent: %v", err)
	}
	if pi.Status != paymentintent.StatusFundsReceived {
		t.Errorf("expected status FUNDS_RECEIVED, got %s", pi.Status)
	}
}

func TestE2E_PaymentIntentRecipient(t *testing.T) {
	srv := newTestServer(t)
	ctx := context.Background()

	body := `{
		"paymentReference": "e2e-ref-1",
		"payInCurrency": "EUR",
		"payInAmount": {"unscaled": 200, "exponent": 0},
		"payOutCurrency": "GBP",
		"payOutDetails": {"sepa": {"iban": "IBAN", "beneficiaryName": "Bob"}}
	}`
	resp := srv.request(t, http.MethodPost, "/api/v1/payment-intents", []byte(body))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("recipient create intent: expected 201, got %d: %s", resp.StatusCode, readBody(t, resp))
	}

	pi, err := srv.paymentIntentStore.Get(ctx, 1, paymentintent.RoleBeneficiary)
	if err != nil {
		t.Fatalf("get beneficiary intent: %v", err)
	}
	if pi.Role != paymentintent.RoleBeneficiary {
		t.Errorf("expected role beneficiary, got %s", pi.Role)
	}

	quoteReq := `{
		"payInCurrency": "EUR",
		"payInAmount": {"unscaled": 200, "exponent": 0},
		"payOutCurrency": "GBP",
		"payInPaymentMethod": "PAYMENT_METHOD_TYPE_SEPA",
		"payOutPaymentMethod": "PAYMENT_METHOD_TYPE_SWIFT"
	}`
	resp = srv.request(t, http.MethodPost, "/api/v1/payment-intent-quotes", []byte(quoteReq))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recipient get quote: expected 200, got %d: %s", resp.StatusCode, readBody(t, resp))
	}
}

func TestE2E_Unauthorized(t *testing.T) {
	srv := newTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, srv.baseURL+"/api/v1/payments/1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

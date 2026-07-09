package quoteapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
	"my-provider/internal/quote"
)

const testAPIKey = "test-api-key"

type mockStore struct {
	snapshots map[quote.StreamType][]quote.QuoteGroup
	err       error
	replaced  []quote.QuoteGroup
}

func (m *mockStore) GetSnapshots(_ context.Context, stream quote.StreamType) ([]quote.QuoteGroup, error) {
	if m.err != nil {
		return nil, m.err
	}
	groups, ok := m.snapshots[stream]
	if !ok || len(groups) == 0 {
		return nil, quote.ErrNotFound
	}
	return groups, nil
}

func (m *mockStore) ReplaceSnapshots(_ context.Context, _ quote.StreamType, groups []quote.QuoteGroup) error {
	if m.err != nil {
		return m.err
	}
	m.replaced = groups
	return nil
}

func (m *mockStore) Close() error { return nil }

type mockPublisher struct {
	publishErr error
	published  int
}

func (m *mockPublisher) Publish(_ context.Context) error {
	m.published++
	return m.publishErr
}

type mockNetwork struct {
	quoteResp *payment.GetQuoteResponse
	err       error
}

func (m *mockNetwork) UpdateQuote(_ context.Context, _ *connect.Request[payment.UpdateQuoteRequest]) (*connect.Response[payment.UpdateQuoteResponse], error) {
	return nil, nil
}

func (m *mockNetwork) GetQuote(_ context.Context, _ *connect.Request[payment.GetQuoteRequest]) (*connect.Response[payment.GetQuoteResponse], error) {
	if m.err != nil {
		return nil, m.err
	}
	return connect.NewResponse(m.quoteResp), nil
}

func (m *mockNetwork) CreatePayment(_ context.Context, _ *connect.Request[payment.CreatePaymentRequest]) (*connect.Response[payment.CreatePaymentResponse], error) {
	return nil, nil
}

//nolint:staticcheck // Required to satisfy paymentconnect.NetworkServiceClient interface.
func (m *mockNetwork) ConfirmPayout(_ context.Context, _ *connect.Request[payment.ConfirmPayoutRequest]) (*connect.Response[payment.ConfirmPayoutResponse], error) {
	return nil, nil
}

func (m *mockNetwork) FinalizePayout(_ context.Context, _ *connect.Request[payment.FinalizePayoutRequest]) (*connect.Response[payment.FinalizePayoutResponse], error) {
	return nil, nil
}

func (m *mockNetwork) CompleteManualAmlCheck(_ context.Context, _ *connect.Request[payment.CompleteManualAmlCheckRequest]) (*connect.Response[payment.CompleteManualAmlCheckResponse], error) {
	return nil, nil
}

func newTestHandler(store quote.Store, pub Publisher, net paymentconnect.NetworkServiceClient) *Handler {
	return NewHandler(store, pub, net, []string{testAPIKey})
}

func authHeader() string {
	return "Bearer " + testAPIKey
}

func TestHandler_GetQuotes(t *testing.T) {
	store := &mockStore{
		snapshots: map[quote.StreamType][]quote.QuoteGroup{
			quote.StreamTypePayOut: {
				{Currency: "EUR", PaymentMethod: "PAYMENT_METHOD_TYPE_SEPA", Expiration: time.Now().Add(time.Hour), Timestamp: time.Now(), Bands: []quote.Band{
					{ClientQuoteID: "eur-1k", MaxAmount: quote.Decimal{Unscaled: 1000, Exponent: 0}, Rate: quote.Decimal{Unscaled: 86, Exponent: -2}},
				}},
			},
		},
	}

	handler := newTestHandler(store, &mockPublisher{}, &mockNetwork{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/quotes", nil)
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp QuotesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(resp.PayOut) != 1 {
		t.Errorf("expected 1 pay-out group, got %d", len(resp.PayOut))
	}
}

func TestHandler_GetQuotesUnauthorized(t *testing.T) {
	handler := newTestHandler(&mockStore{}, &mockPublisher{}, &mockNetwork{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/quotes", nil)
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
}

func TestHandler_UpdatePayIn(t *testing.T) {
	store := &mockStore{}
	pub := &mockPublisher{}
	handler := newTestHandler(store, pub, &mockNetwork{})

	body := `{"groups":[{"currency":"EUR","paymentMethod":"PAYMENT_METHOD_TYPE_SEPA","expiration":"2099-01-01T00:00:00Z","timestamp":"2099-01-01T00:00:00Z","bands":[{"clientQuoteId":"eur-payin-1k","maxAmount":{"unscaled":1000,"exponent":0},"rate":{"unscaled":88,"exponent":-2}}]}]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/quotes/pay-in", strings.NewReader(body))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(store.replaced) != 1 {
		t.Errorf("expected 1 replaced group, got %d", len(store.replaced))
	}
	if pub.published != 1 {
		t.Errorf("expected 1 publish call, got %d", pub.published)
	}
}

func TestHandler_UpdatePayOut(t *testing.T) {
	store := &mockStore{}
	pub := &mockPublisher{}
	handler := newTestHandler(store, pub, &mockNetwork{})

	body := `{"groups":[{"currency":"EUR","paymentMethod":"PAYMENT_METHOD_TYPE_SEPA","expiration":"2099-01-01T00:00:00Z","timestamp":"2099-01-01T00:00:00Z","bands":[{"clientQuoteId":"eur-1k","maxAmount":{"unscaled":1000,"exponent":0},"rate":{"unscaled":86,"exponent":-2}}]}]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/quotes/pay-out", strings.NewReader(body))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(store.replaced) != 1 {
		t.Errorf("expected 1 replaced group, got %d", len(store.replaced))
	}
	if pub.published != 1 {
		t.Errorf("expected 1 publish call, got %d", pub.published)
	}
}

func TestHandler_UpdatePayOutInvalid(t *testing.T) {
	store := &mockStore{}
	pub := &mockPublisher{}
	handler := newTestHandler(store, pub, &mockNetwork{})

	body := `{"groups":[{"currency":"EUR","paymentMethod":"PAYMENT_METHOD_TYPE_SEPA","expiration":"2099-01-01T00:00:00Z","timestamp":"2099-01-01T00:00:00Z","bands":[{"clientQuoteId":"","maxAmount":{"unscaled":1000,"exponent":0},"rate":{"unscaled":86,"exponent":-2}}]}]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/quotes/pay-out", strings.NewReader(body))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
	if len(store.replaced) != 0 {
		t.Errorf("expected no replace, got %d", len(store.replaced))
	}
}

func TestHandler_Publish(t *testing.T) {
	pub := &mockPublisher{}
	handler := newTestHandler(&mockStore{}, pub, &mockNetwork{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/quotes/publish", nil)
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if pub.published != 1 {
		t.Errorf("expected 1 publish call, got %d", pub.published)
	}
}

func TestHandler_GetNetworkQuote(t *testing.T) {
	net := &mockNetwork{
		quoteResp: &payment.GetQuoteResponse{
			Result: &payment.GetQuoteResponse_Success_{Success: &payment.GetQuoteResponse_Success{
				Rate:             &common.Decimal{Unscaled: 86, Exponent: -2},
				PayOutAmount:     &common.Decimal{Unscaled: 500, Exponent: 0},
				SettlementAmount: &common.Decimal{Unscaled: 500, Exponent: 0},
			}},
		},
	}
	handler := newTestHandler(&mockStore{}, &mockPublisher{}, net)

	body := `{"amount":{"unscaled":500,"exponent":0},"amountType":"settlement","payOutCurrency":"GBP","payOutMethod":"PAYMENT_METHOD_TYPE_SWIFT"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/quotes/network", strings.NewReader(body))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if _, ok := resp["Result"]; !ok {
		t.Errorf("expected Result field in response")
	}
}

func TestHandler_GetNetworkQuotePayOutAmount(t *testing.T) {
	net := &mockNetwork{
		quoteResp: &payment.GetQuoteResponse{
			Result: &payment.GetQuoteResponse_Success_{Success: &payment.GetQuoteResponse_Success{
				Rate:             &common.Decimal{Unscaled: 79, Exponent: -2},
				PayOutAmount:     &common.Decimal{Unscaled: 400, Exponent: 0},
				SettlementAmount: &common.Decimal{Unscaled: 500, Exponent: 0},
			}},
		},
	}
	handler := newTestHandler(&mockStore{}, &mockPublisher{}, net)

	body := `{"amount":{"unscaled":500,"exponent":0},"amountType":"pay_out","payOutCurrency":"GBP","payOutMethod":"PAYMENT_METHOD_TYPE_SWIFT"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/quotes/network", strings.NewReader(body))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandler_GetNetworkQuoteInvalidAmountType(t *testing.T) {
	handler := newTestHandler(&mockStore{}, &mockPublisher{}, &mockNetwork{})

	body := `{"amount":{"unscaled":500,"exponent":0},"amountType":"invalid","payOutCurrency":"GBP","payOutMethod":"PAYMENT_METHOD_TYPE_SWIFT"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/quotes/network", strings.NewReader(body))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandler_StoreError(t *testing.T) {
	store := &mockStore{err: errors.New("db down")}
	handler := newTestHandler(store, &mockPublisher{}, &mockNetwork{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/quotes", nil)
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rr.Code)
	}
}

func TestHandler_InvalidAPIKey(t *testing.T) {
	handler := newTestHandler(&mockStore{}, &mockPublisher{}, &mockNetwork{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/quotes", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}
}

func TestHandler_UpdatePayOutInvalidJSON(t *testing.T) {
	handler := newTestHandler(&mockStore{}, &mockPublisher{}, &mockNetwork{})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/quotes/pay-out", strings.NewReader("not-json"))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandler_UpdatePayOutMissingGroups(t *testing.T) {
	handler := newTestHandler(&mockStore{}, &mockPublisher{}, &mockNetwork{})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/quotes/pay-out", strings.NewReader(`{}`))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandler_UpdatePayOutStoreError(t *testing.T) {
	store := &mockStore{err: errors.New("db down")}
	handler := newTestHandler(store, &mockPublisher{}, &mockNetwork{})

	body := `{"groups":[{"currency":"EUR","paymentMethod":"PAYMENT_METHOD_TYPE_SEPA","expiration":"2099-01-01T00:00:00Z","timestamp":"2099-01-01T00:00:00Z","bands":[{"clientQuoteId":"eur-1k","maxAmount":{"unscaled":1000,"exponent":0},"rate":{"unscaled":86,"exponent":-2}}]}]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/quotes/pay-out", strings.NewReader(body))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rr.Code)
	}
}

func TestHandler_UpdatePayOutPublishError(t *testing.T) {
	store := &mockStore{}
	pub := &mockPublisher{publishErr: errors.New("network down")}
	handler := newTestHandler(store, pub, &mockNetwork{})

	body := `{"groups":[{"currency":"EUR","paymentMethod":"PAYMENT_METHOD_TYPE_SEPA","expiration":"2099-01-01T00:00:00Z","timestamp":"2099-01-01T00:00:00Z","bands":[{"clientQuoteId":"eur-1k","maxAmount":{"unscaled":1000,"exponent":0},"rate":{"unscaled":86,"exponent":-2}}]}]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/quotes/pay-out", strings.NewReader(body))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
	if len(store.replaced) != 1 {
		t.Errorf("expected snapshot saved, got %d replaced", len(store.replaced))
	}
	if pub.published != 1 {
		t.Errorf("expected 1 publish attempt, got %d", pub.published)
	}
}

func TestHandler_PublishError(t *testing.T) {
	pub := &mockPublisher{publishErr: errors.New("publish failed")}
	handler := newTestHandler(&mockStore{}, pub, &mockNetwork{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/quotes/publish", nil)
	req.Header.Set("Authorization", authHeader())
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rr.Code)
	}
}

func TestHandler_GetNetworkQuoteInvalidMethod(t *testing.T) {
	handler := newTestHandler(&mockStore{}, &mockPublisher{}, &mockNetwork{})

	body := `{"amount":{"unscaled":500,"exponent":0},"amountType":"settlement","payOutCurrency":"GBP","payOutMethod":"INVALID_METHOD"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/quotes/network", strings.NewReader(body))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestHandler_GetNetworkQuoteNetworkError(t *testing.T) {
	net := &mockNetwork{err: errors.New("network down")}
	handler := newTestHandler(&mockStore{}, &mockPublisher{}, net)

	body := `{"amount":{"unscaled":500,"exponent":0},"amountType":"settlement","payOutCurrency":"GBP","payOutMethod":"PAYMENT_METHOD_TYPE_SWIFT"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/quotes/network", strings.NewReader(body))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected status 502, got %d", rr.Code)
	}
}

func TestHandler_GetNetworkQuoteMissingAmount(t *testing.T) {
	handler := newTestHandler(&mockStore{}, &mockPublisher{}, &mockNetwork{})

	body := `{"amountType":"settlement","payOutCurrency":"GBP","payOutMethod":"PAYMENT_METHOD_TYPE_SWIFT"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/quotes/network", strings.NewReader(body))
	req.Header.Set("Authorization", authHeader())
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

package quote

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
)

type mockNetworkClient struct {
	updateQuoteReq *payment.UpdateQuoteRequest
	updateQuoteErr error
}

func (m *mockNetworkClient) UpdateQuote(_ context.Context, req *connect.Request[payment.UpdateQuoteRequest]) (*connect.Response[payment.UpdateQuoteResponse], error) {
	m.updateQuoteReq = req.Msg
	if m.updateQuoteErr != nil {
		return nil, m.updateQuoteErr
	}
	return connect.NewResponse(&payment.UpdateQuoteResponse{}), nil
}

func (m *mockNetworkClient) GetQuote(_ context.Context, _ *connect.Request[payment.GetQuoteRequest]) (*connect.Response[payment.GetQuoteResponse], error) {
	return nil, nil
}

func (m *mockNetworkClient) CreatePayment(_ context.Context, _ *connect.Request[payment.CreatePaymentRequest]) (*connect.Response[payment.CreatePaymentResponse], error) {
	return nil, nil
}

//nolint:staticcheck // Required to satisfy paymentconnect.NetworkServiceClient interface.
func (m *mockNetworkClient) ConfirmPayout(_ context.Context, _ *connect.Request[payment.ConfirmPayoutRequest]) (*connect.Response[payment.ConfirmPayoutResponse], error) {
	return nil, nil
}

func (m *mockNetworkClient) FinalizePayout(_ context.Context, _ *connect.Request[payment.FinalizePayoutRequest]) (*connect.Response[payment.FinalizePayoutResponse], error) {
	return nil, nil
}

func (m *mockNetworkClient) CompleteManualAmlCheck(_ context.Context, _ *connect.Request[payment.CompleteManualAmlCheckRequest]) (*connect.Response[payment.CompleteManualAmlCheckResponse], error) {
	return nil, nil
}

func TestPublisher_PublishWithDefaults(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	net := &mockNetworkClient{}
	publisher := NewPublisher(store, net, true, false)

	if err := publisher.Publish(context.Background()); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	if net.updateQuoteReq == nil {
		t.Fatal("expected UpdateQuote request")
	}
	if len(net.updateQuoteReq.PayOut) != 1 {
		t.Errorf("expected 1 pay-out group, got %d", len(net.updateQuoteReq.PayOut))
	}
	if len(net.updateQuoteReq.PayIn) != 0 {
		t.Errorf("expected 0 pay-in groups, got %d", len(net.updateQuoteReq.PayIn))
	}
}

func TestPublisher_PublishStoredQuotes(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	now := time.Now().UTC()
	groups := []QuoteGroup{
		{Currency: "GBP", PaymentMethod: "PAYMENT_METHOD_TYPE_SWIFT", Expiration: now.Add(time.Hour), Timestamp: now, Bands: []Band{
			{ClientQuoteID: "gbp-swift-1k", MaxAmount: Decimal{Unscaled: 1000, Exponent: 0}, Rate: Decimal{Unscaled: 79, Exponent: -2}},
		}},
	}
	if err := store.ReplaceSnapshots(context.Background(), StreamTypePayOut, groups); err != nil {
		t.Fatalf("ReplaceSnapshots() error = %v", err)
	}

	net := &mockNetworkClient{}
	publisher := NewPublisher(store, net, false, false)

	if err := publisher.Publish(context.Background()); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	if net.updateQuoteReq == nil {
		t.Fatal("expected UpdateQuote request")
	}
	if len(net.updateQuoteReq.PayOut) != 1 || net.updateQuoteReq.PayOut[0].Currency != "GBP" {
		t.Errorf("expected GBP pay-out group, got %+v", net.updateQuoteReq.PayOut)
	}
}

func TestPublisher_PublishNetworkError(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	net := &mockNetworkClient{updateQuoteErr: errors.New("network error")}
	publisher := NewPublisher(store, net, true, false)

	if err := publisher.Publish(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestPublisher_FiltersExpiredQuotes(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	now := time.Now().UTC()
	expired := []QuoteGroup{
		{Currency: "EUR", PaymentMethod: "PAYMENT_METHOD_TYPE_SEPA", Expiration: now.Add(-time.Hour), Timestamp: now.Add(-2 * time.Hour), Bands: []Band{
			{ClientQuoteID: "expired", MaxAmount: Decimal{Unscaled: 1000, Exponent: 0}, Rate: Decimal{Unscaled: 86, Exponent: -2}},
		}},
	}
	if err := store.ReplaceSnapshots(context.Background(), StreamTypePayOut, expired); err != nil {
		t.Fatalf("ReplaceSnapshots() error = %v", err)
	}

	net := &mockNetworkClient{}
	publisher := NewPublisher(store, net, false, false)

	if err := publisher.Publish(context.Background()); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	if len(net.updateQuoteReq.PayOut) != 0 {
		t.Errorf("expected expired pay-out groups to be filtered, got %d", len(net.updateQuoteReq.PayOut))
	}
}

func TestPublisher_RegeneratesExpiredDefaults(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	now := time.Now().UTC()
	expired := []QuoteGroup{
		{Currency: "EUR", PaymentMethod: "PAYMENT_METHOD_TYPE_SEPA", Expiration: now.Add(-time.Hour), Timestamp: now.Add(-2 * time.Hour), Bands: []Band{
			{ClientQuoteID: "expired", MaxAmount: Decimal{Unscaled: 1000, Exponent: 0}, Rate: Decimal{Unscaled: 86, Exponent: -2}},
		}},
	}
	if err := store.ReplaceSnapshots(context.Background(), StreamTypePayOut, expired); err != nil {
		t.Fatalf("ReplaceSnapshots() error = %v", err)
	}

	net := &mockNetworkClient{}
	publisher := NewPublisher(store, net, true, false)

	if err := publisher.Publish(context.Background()); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	if len(net.updateQuoteReq.PayOut) != 1 {
		t.Errorf("expected default pay-out group to be regenerated, got %d", len(net.updateQuoteReq.PayOut))
	}
}

// Ensure mockNetworkClient implements the interface at compile time.
var _ paymentconnect.NetworkServiceClient = (*mockNetworkClient)(nil)

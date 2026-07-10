package handler

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
	localpayment "my-provider/internal/payment"
	"my-provider/internal/settlement"
)

type paymentNetwork struct {
	completed []uint64
}

func (n *paymentNetwork) UpdateQuote(_ context.Context, _ *connect.Request[payment.UpdateQuoteRequest]) (*connect.Response[payment.UpdateQuoteResponse], error) {
	return nil, nil
}

func (n *paymentNetwork) GetQuote(_ context.Context, _ *connect.Request[payment.GetQuoteRequest]) (*connect.Response[payment.GetQuoteResponse], error) {
	return nil, nil
}

func (n *paymentNetwork) CreatePayment(_ context.Context, _ *connect.Request[payment.CreatePaymentRequest]) (*connect.Response[payment.CreatePaymentResponse], error) {
	return nil, nil
}

//nolint:staticcheck // Required to satisfy paymentconnect.NetworkServiceClient interface.
func (n *paymentNetwork) ConfirmPayout(_ context.Context, _ *connect.Request[payment.ConfirmPayoutRequest]) (*connect.Response[payment.ConfirmPayoutResponse], error) {
	return nil, nil
}

func (n *paymentNetwork) FinalizePayout(_ context.Context, _ *connect.Request[payment.FinalizePayoutRequest]) (*connect.Response[payment.FinalizePayoutResponse], error) {
	return nil, nil
}

func (n *paymentNetwork) CompleteManualAmlCheck(_ context.Context, req *connect.Request[payment.CompleteManualAmlCheckRequest]) (*connect.Response[payment.CompleteManualAmlCheckResponse], error) {
	n.completed = append(n.completed, req.Msg.PaymentId)
	return connect.NewResponse(&payment.CompleteManualAmlCheckResponse{}), nil
}

var _ paymentconnect.NetworkServiceClient = (*paymentNetwork)(nil)

func newTestStore(t *testing.T) localpayment.Store {
	t.Helper()
	store, err := localpayment.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestProviderServiceImplementation_PayOut(t *testing.T) {
	store := newTestStore(t)

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0)
	resp, err := svc.PayOut(context.Background(), connect.NewRequest(&payment.PayoutRequest{
		PaymentId: 42,
	}))
	if err != nil {
		t.Fatalf("PayOut() error = %v", err)
	}
	if _, ok := resp.Msg.Result.(*payment.PayoutResponse_ManualAmlCheck_); !ok {
		t.Errorf("expected ManualAmlCheck result, got %T", resp.Msg.Result)
	}

	p, err := store.GetByPaymentID(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetByPaymentID() error = %v", err)
	}
	if p.Status != localpayment.StatusPayoutRequested {
		t.Errorf("expected status %s, got %s", localpayment.StatusPayoutRequested, p.Status)
	}
	if p.Role != localpayment.RoleProvider {
		t.Errorf("expected role %s, got %s", localpayment.RoleProvider, p.Role)
	}
}

func TestProviderServiceImplementation_UpdatePayment(t *testing.T) {
	store := newTestStore(t)

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0)

	if _, err := svc.PayOut(context.Background(), connect.NewRequest(&payment.PayoutRequest{PaymentId: 7})); err != nil {
		t.Fatalf("PayOut() error = %v", err)
	}

	resp, err := svc.UpdatePayment(context.Background(), connect.NewRequest(&payment.UpdatePaymentRequest{
		PaymentId:       7,
		PaymentClientId: "client-7",
		Result:          &payment.UpdatePaymentRequest_Accepted_{Accepted: &payment.UpdatePaymentRequest_Accepted{}},
	}))
	if err != nil {
		t.Fatalf("UpdatePayment() error = %v", err)
	}
	if resp.Msg == nil {
		t.Fatal("expected non-nil response")
	}

	p, err := store.GetByPaymentID(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetByPaymentID() error = %v", err)
	}
	if p.Status != localpayment.StatusAccepted {
		t.Errorf("expected status %s, got %s", localpayment.StatusAccepted, p.Status)
	}
}

func TestProviderServiceImplementation_UpdatePaymentUnknown(t *testing.T) {
	store := newTestStore(t)

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0)

	resp, err := svc.UpdatePayment(context.Background(), connect.NewRequest(&payment.UpdatePaymentRequest{
		PaymentId:       99,
		PaymentClientId: "client-99",
		Result:          &payment.UpdatePaymentRequest_Failed_{Failed: &payment.UpdatePaymentRequest_Failed{}},
	}))
	if err != nil {
		t.Fatalf("UpdatePayment() error = %v", err)
	}
	if resp.Msg == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestProviderServiceImplementation_UpdateLimit(t *testing.T) {
	store := newTestStore(t)

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0)
	resp, err := svc.UpdateLimit(context.Background(), connect.NewRequest(&payment.UpdateLimitRequest{}))
	if err != nil {
		t.Fatalf("UpdateLimit() error = %v", err)
	}
	if resp.Msg == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestProviderServiceImplementation_AppendLedgerEntries(t *testing.T) {
	store := newTestStore(t)

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0)
	resp, err := svc.AppendLedgerEntries(context.Background(), connect.NewRequest(&payment.AppendLedgerEntriesRequest{}))
	if err != nil {
		t.Fatalf("AppendLedgerEntries() error = %v", err)
	}
	if resp.Msg == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestProviderServiceImplementation_ApprovePaymentQuotes(t *testing.T) {
	store := newTestStore(t)

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 1.0)

	// Seed a provider payment with a known payout amount.
	_, err := store.Create(context.Background(), localpayment.Payment{
		PaymentClientID: "client-approve",
		Role:            localpayment.RoleProvider,
		Status:          localpayment.StatusPayoutRequested,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(100)),
		PayoutAmount:    &localpayment.Decimal{Unscaled: 1000, Exponent: 0},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Matching amount should be accepted.
	resp, err := svc.ApprovePaymentQuotes(context.Background(), connect.NewRequest(&payment.ApprovePaymentQuoteRequest{
		PaymentId:    100,
		PayOutAmount: &common.Decimal{Unscaled: 1000, Exponent: 0},
	}))
	if err != nil {
		t.Fatalf("ApprovePaymentQuotes() error = %v", err)
	}
	if _, ok := resp.Msg.Result.(*payment.ApprovePaymentQuoteResponse_Accepted_); !ok {
		t.Errorf("expected Accepted, got %T", resp.Msg.Result)
	}

	// Amount outside 1% tolerance should be rejected.
	resp, err = svc.ApprovePaymentQuotes(context.Background(), connect.NewRequest(&payment.ApprovePaymentQuoteRequest{
		PaymentId:    100,
		PayOutAmount: &common.Decimal{Unscaled: 1100, Exponent: 0},
	}))
	if err != nil {
		t.Fatalf("ApprovePaymentQuotes() error = %v", err)
	}
	if _, ok := resp.Msg.Result.(*payment.ApprovePaymentQuoteResponse_Rejected_); !ok {
		t.Errorf("expected Rejected, got %T", resp.Msg.Result)
	}
}

func ptr[T any](v T) *T {
	return &v
}

func TestApproveAmlAfter(t *testing.T) {
	store := newTestStore(t)

	net := &paymentNetwork{}
	svc := NewProviderServiceImplementation(net, store, nil, settlement.NewNoOpNotifier(), 0)

	svc.approveAmlAfter(5, 10*time.Millisecond)

	if len(net.completed) != 1 || net.completed[0] != 5 {
		t.Errorf("expected CompleteManualAmlCheck for payment 5, got %v", net.completed)
	}
}

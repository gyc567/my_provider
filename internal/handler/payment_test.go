package handler

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
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

func clearPayments() {
	payments.Range(func(key, _ any) bool {
		payments.Delete(key)
		return true
	})
}

func TestProviderServiceImplementation_PayOut(t *testing.T) {
	clearPayments()
	defer clearPayments()

	svc := NewProviderServiceImplementation(&paymentNetwork{})
	resp, err := svc.PayOut(context.Background(), connect.NewRequest(&payment.PayoutRequest{
		PaymentId: 42,
	}))
	if err != nil {
		t.Fatalf("PayOut() error = %v", err)
	}
	if _, ok := resp.Msg.Result.(*payment.PayoutResponse_ManualAmlCheck_); !ok {
		t.Errorf("expected ManualAmlCheck result, got %T", resp.Msg.Result)
	}

	if _, ok := payments.Load(uint64(42)); !ok {
		t.Error("expected payment to be tracked")
	}
}

func TestProviderServiceImplementation_UpdatePayment(t *testing.T) {
	clearPayments()
	defer clearPayments()

	svc := NewProviderServiceImplementation(&paymentNetwork{})

	// First receive a payout so the payment is tracked.
	if _, err := svc.PayOut(context.Background(), connect.NewRequest(&payment.PayoutRequest{PaymentId: 7})); err != nil {
		t.Fatalf("PayOut() error = %v", err)
	}

	resp, err := svc.UpdatePayment(context.Background(), connect.NewRequest(&payment.UpdatePaymentRequest{
		PaymentId:      7,
		PaymentClientId: "client-7",
		Result:         &payment.UpdatePaymentRequest_Accepted_{Accepted: &payment.UpdatePaymentRequest_Accepted{}},
	}))
	if err != nil {
		t.Fatalf("UpdatePayment() error = %v", err)
	}
	if resp.Msg == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestProviderServiceImplementation_UpdatePaymentUnknown(t *testing.T) {
	clearPayments()
	defer clearPayments()

	svc := NewProviderServiceImplementation(&paymentNetwork{})

	resp, err := svc.UpdatePayment(context.Background(), connect.NewRequest(&payment.UpdatePaymentRequest{
		PaymentId:      99,
		PaymentClientId: "client-99",
		Result:         &payment.UpdatePaymentRequest_Failed_{Failed: &payment.UpdatePaymentRequest_Failed{}},
	}))
	if err != nil {
		t.Fatalf("UpdatePayment() error = %v", err)
	}
	if resp.Msg == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestProviderServiceImplementation_UpdateLimit(t *testing.T) {
	svc := NewProviderServiceImplementation(&paymentNetwork{})
	resp, err := svc.UpdateLimit(context.Background(), connect.NewRequest(&payment.UpdateLimitRequest{}))
	if err != nil {
		t.Fatalf("UpdateLimit() error = %v", err)
	}
	if resp.Msg == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestProviderServiceImplementation_AppendLedgerEntries(t *testing.T) {
	svc := NewProviderServiceImplementation(&paymentNetwork{})
	resp, err := svc.AppendLedgerEntries(context.Background(), connect.NewRequest(&payment.AppendLedgerEntriesRequest{}))
	if err != nil {
		t.Fatalf("AppendLedgerEntries() error = %v", err)
	}
	if resp.Msg == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestProviderServiceImplementation_ApprovePaymentQuotes(t *testing.T) {
	svc := NewProviderServiceImplementation(&paymentNetwork{})
	resp, err := svc.ApprovePaymentQuotes(context.Background(), connect.NewRequest(&payment.ApprovePaymentQuoteRequest{}))
	if err != nil {
		t.Fatalf("ApprovePaymentQuotes() error = %v", err)
	}
	if resp.Msg == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestApproveAmlAfter(t *testing.T) {
	clearPayments()
	defer clearPayments()

	net := &paymentNetwork{}
	svc := NewProviderServiceImplementation(net)

	svc.approveAmlAfter(5, 10*time.Millisecond)

	if len(net.completed) != 1 || net.completed[0] != 5 {
		t.Errorf("expected CompleteManualAmlCheck for payment 5, got %v", net.completed)
	}
}

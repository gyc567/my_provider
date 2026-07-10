package payment

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	sdkpayment "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
)

type timeoutFakeNetwork struct {
	attempts atomic.Int32
	delay    time.Duration
	errAfter int32 // fail first N calls
}

func (f *timeoutFakeNetwork) UpdateQuote(_ context.Context, _ *connect.Request[sdkpayment.UpdateQuoteRequest]) (*connect.Response[sdkpayment.UpdateQuoteResponse], error) {
	return nil, nil
}

func (f *timeoutFakeNetwork) GetQuote(_ context.Context, _ *connect.Request[sdkpayment.GetQuoteRequest]) (*connect.Response[sdkpayment.GetQuoteResponse], error) {
	return nil, nil
}

func (f *timeoutFakeNetwork) CreatePayment(ctx context.Context, _ *connect.Request[sdkpayment.CreatePaymentRequest]) (*connect.Response[sdkpayment.CreatePaymentResponse], error) {
	_ = f.attempts.Add(1)
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	return connect.NewResponse(&sdkpayment.CreatePaymentResponse{}), nil
}

//nolint:staticcheck // Required to satisfy paymentconnect.NetworkServiceClient interface.
func (f *timeoutFakeNetwork) ConfirmPayout(_ context.Context, _ *connect.Request[sdkpayment.ConfirmPayoutRequest]) (*connect.Response[sdkpayment.ConfirmPayoutResponse], error) {
	return nil, nil
}

func (f *timeoutFakeNetwork) FinalizePayout(ctx context.Context, _ *connect.Request[sdkpayment.FinalizePayoutRequest]) (*connect.Response[sdkpayment.FinalizePayoutResponse], error) {
	count := f.attempts.Add(1)
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	if f.errAfter > 0 && count <= f.errAfter {
		return nil, errors.New("finalize error")
	}
	return connect.NewResponse(&sdkpayment.FinalizePayoutResponse{}), nil
}

func (f *timeoutFakeNetwork) CompleteManualAmlCheck(ctx context.Context, _ *connect.Request[sdkpayment.CompleteManualAmlCheckRequest]) (*connect.Response[sdkpayment.CompleteManualAmlCheckResponse], error) {
	count := f.attempts.Add(1)
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	if f.errAfter > 0 && count <= f.errAfter {
		return nil, errors.New("aml error")
	}
	return connect.NewResponse(&sdkpayment.CompleteManualAmlCheckResponse{}), nil
}

var _ paymentconnect.NetworkServiceClient = (*timeoutFakeNetwork)(nil)

func TestNetworkClient_TimeoutApplied(t *testing.T) {
	fake := &timeoutFakeNetwork{delay: 200 * time.Millisecond}
	client := NewNetworkClientWithTimeout(fake, 50*time.Millisecond)

	// CreatePayment has timeout but no retry, so only one attempt is made.
	_, err := client.CreatePayment(context.Background(), CreateRequest{
		PaymentClientID: "c1",
		Amount:          Decimal{Unscaled: 100, Exponent: 0},
		AmountType:      "pay_out",
		Currency:        "GBP",
		PaymentMethod:   "PAYMENT_METHOD_TYPE_SWIFT",
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if fake.attempts.Load() != 1 {
		t.Errorf("expected 1 attempt, got %d", fake.attempts.Load())
	}
}

func TestNetworkClient_RetrySucceeds(t *testing.T) {
	fake := &timeoutFakeNetwork{errAfter: 1}
	client := NewNetworkClientWithTimeout(fake, time.Second)

	err := client.CompleteManualAmlCheck(context.Background(), 1, true, "")
	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if fake.attempts.Load() != 2 {
		t.Errorf("expected 2 attempts, got %d", fake.attempts.Load())
	}
}

func TestNetworkClient_RetryExhausted(t *testing.T) {
	fake := &timeoutFakeNetwork{errAfter: 5}
	client := NewNetworkClientWithTimeout(fake, time.Second)

	err := client.FinalizePayout(context.Background(), 1, FinalizeRequest{Success: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if fake.attempts.Load() != 2 {
		t.Errorf("expected 2 attempts (initial + 1 retry), got %d", fake.attempts.Load())
	}
}

func TestNetworkClient_CreatePaymentTimeout(t *testing.T) {
	fake := &timeoutFakeNetwork{}
	client := NewNetworkClientWithTimeout(fake, time.Second)

	_, err := client.CreatePayment(context.Background(), CreateRequest{
		PaymentClientID: "c1",
		Amount:          Decimal{Unscaled: 100, Exponent: 0},
		AmountType:      "pay_out",
		Currency:        "GBP",
		PaymentMethod:   "PAYMENT_METHOD_TYPE_SWIFT",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNetworkClient_CreatePaymentSettlementAmount(t *testing.T) {
	fake := &timeoutFakeNetwork{}
	client := NewNetworkClientWithTimeout(fake, time.Second)

	_, err := client.CreatePayment(context.Background(), CreateRequest{
		PaymentClientID: "c2",
		Amount:          Decimal{Unscaled: 100, Exponent: 0},
		AmountType:      "settlement",
		Currency:        "EUR",
		PaymentMethod:   "PAYMENT_METHOD_TYPE_SEPA",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNetworkClient_DefaultTimeout(t *testing.T) {
	fake := &timeoutFakeNetwork{}
	client := NewNetworkClientWithTimeout(fake, 0)
	if client.timeout != defaultNetworkTimeout {
		t.Errorf("expected default timeout %v, got %v", defaultNetworkTimeout, client.timeout)
	}
}

func TestNetworkClient_withTimeoutZero(t *testing.T) {
	fake := &timeoutFakeNetwork{}
	client := &NetworkClient{client: fake, timeout: 0}
	ctx, cancel := client.withTimeout(context.Background())
	defer cancel()
	deadline, ok := ctx.Deadline()
	if ok {
		t.Errorf("expected no deadline, got %v", deadline)
	}
}

func TestNetworkClient_FinalizePayout_SuccessWithReceipt(t *testing.T) {
	fake := &timeoutFakeNetwork{}
	client := NewNetworkClientWithTimeout(fake, time.Second)

	err := client.FinalizePayout(context.Background(), 42, FinalizeRequest{Success: true, Receipt: "receipt-42"})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if fake.attempts.Load() != 1 {
		t.Errorf("expected 1 attempt, got %d", fake.attempts.Load())
	}
}

func TestNetworkClient_FinalizePayout_Failure(t *testing.T) {
	fake := &timeoutFakeNetwork{}
	client := NewNetworkClientWithTimeout(fake, time.Second)

	err := client.FinalizePayout(context.Background(), 43, FinalizeRequest{Success: false, RejectReason: "rejected"})
	if err != nil {
		t.Fatalf("expected success for failure report, got %v", err)
	}
	if fake.attempts.Load() != 1 {
		t.Errorf("expected 1 attempt, got %d", fake.attempts.Load())
	}
}

func TestNetworkClient_CreatePayment_TravelRuleParseError(t *testing.T) {
	fake := &timeoutFakeNetwork{}
	client := NewNetworkClientWithTimeout(fake, time.Second)

	_, err := client.CreatePayment(context.Background(), CreateRequest{
		PaymentClientID: "c1",
		Amount:          Decimal{Unscaled: 100, Exponent: 0},
		AmountType:      "pay_out",
		Currency:        "GBP",
		PaymentMethod:   "PAYMENT_METHOD_TYPE_SWIFT",
		TravelRuleData:  JSONRaw(`not valid json`),
	})
	if err == nil {
		t.Fatal("expected travel rule parse error")
	}
}

func TestFromCommonDecimal_Nil(t *testing.T) {
	if fromCommonDecimal(nil) != nil {
		t.Error("expected nil for nil input")
	}
}

func TestBuildPaymentDetails_AllMethods(t *testing.T) {
	tests := []struct {
		name   string
		method string
		raw    JSONRaw
	}{
		{
			name:   "SEPA",
			method: "PAYMENT_METHOD_TYPE_SEPA",
			raw:    JSONRaw(`{"iban":"IBAN","beneficiaryName":"Bob","paymentReference":"REF"}`),
		},
		{
			name:   "SWIFT",
			method: "SWIFT",
			raw:    JSONRaw(`{"accountNumber":"ACC","beneficiaryName":"Bob","swiftCode":"SWIFT","paymentReference":"REF"}`),
		},
		{
			name:   "FPS",
			method: "FPS",
			raw:    JSONRaw(`{"sortCode":"SC","accountNumber":"ACC","beneficiaryName":"Bob","reference":"REF"}`),
		},
		{
			name:   "ACH",
			method: "PAYMENT_METHOD_TYPE_ACH",
			raw:    JSONRaw(`{"accountNumber":"ACC","beneficiaryName":"Bob","routingNumber":"RN"}`),
		},
		{
			name:   "unknown",
			method: "PAYMENT_METHOD_TYPE_UNKNOWN",
			raw:    JSONRaw(`{}`),
		},
		{
			name:   "empty raw",
			method: "SEPA",
			raw:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := buildPaymentDetails(tt.method, tt.raw)
			if details == nil {
				t.Fatal("expected non-nil details")
			}
		})
	}
}

func TestBuildPaymentReceipt(t *testing.T) {
	receipt := "receipt-123"
	r := buildPaymentReceipt(receipt)
	if r == nil {
		t.Fatal("expected non-nil receipt")
	}
	sep, ok := r.Details.(*common.PaymentReceipt_Sepa_)
	if !ok {
		t.Fatalf("expected SEPA receipt, got %T", r.Details)
	}
	if sep.Sepa.BankingTransactionReferenceId == nil || *sep.Sepa.BankingTransactionReferenceId != receipt {
		t.Errorf("expected receipt id %s, got %v", receipt, sep.Sepa.BankingTransactionReferenceId)
	}
}

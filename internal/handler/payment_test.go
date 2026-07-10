package handler

import (
	"context"
	"errors"
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

type recordingAMLNotifier struct {
	manualAmlCheckRequired []localpayment.Payment
	amlApproved            []localpayment.Payment
	amlRejected            []localpayment.Payment
	quoteConfirmed         []localpayment.Payment
	quoteRejected          []localpayment.Payment
}

func (r *recordingAMLNotifier) ManualAmlCheckRequired(_ context.Context, p localpayment.Payment) error {
	r.manualAmlCheckRequired = append(r.manualAmlCheckRequired, p)
	return nil
}

func (r *recordingAMLNotifier) AmlApproved(_ context.Context, p localpayment.Payment) error {
	r.amlApproved = append(r.amlApproved, p)
	return nil
}

func (r *recordingAMLNotifier) AmlRejected(_ context.Context, p localpayment.Payment, _ string) error {
	r.amlRejected = append(r.amlRejected, p)
	return nil
}

func (r *recordingAMLNotifier) QuoteConfirmed(_ context.Context, p localpayment.Payment) error {
	r.quoteConfirmed = append(r.quoteConfirmed, p)
	return nil
}

func (r *recordingAMLNotifier) QuoteRejected(_ context.Context, p localpayment.Payment, _ string) error {
	r.quoteRejected = append(r.quoteRejected, p)
	return nil
}

var _ localpayment.AMLNotifier = (*recordingAMLNotifier)(nil)

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

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())
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
	if p.Status != localpayment.StatusManualAmlCheck {
		t.Errorf("expected status %s, got %s", localpayment.StatusManualAmlCheck, p.Status)
	}
	if p.Role != localpayment.RoleProvider {
		t.Errorf("expected role %s, got %s", localpayment.RoleProvider, p.Role)
	}
}

func TestProviderServiceImplementation_PayOut_NotifiesWhenAutoApproveOff(t *testing.T) {
	store := newTestStore(t)
	notifier := &recordingAMLNotifier{}

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0, false, notifier)
	if _, err := svc.PayOut(context.Background(), connect.NewRequest(&payment.PayoutRequest{PaymentId: 99})); err != nil {
		t.Fatalf("PayOut() error = %v", err)
	}

	if len(notifier.manualAmlCheckRequired) != 1 {
		t.Fatalf("expected 1 manual aml notification, got %d", len(notifier.manualAmlCheckRequired))
	}
	if notifier.manualAmlCheckRequired[0].Status != localpayment.StatusManualAmlCheck {
		t.Errorf("expected notified payment status %s, got %s", localpayment.StatusManualAmlCheck, notifier.manualAmlCheckRequired[0].Status)
	}
}

func TestProviderServiceImplementation_PayOut_AutoApproveOn(t *testing.T) {
	store := newTestStore(t)
	net := &paymentNetwork{}
	notifier := &recordingAMLNotifier{}

	svc := NewProviderServiceImplementation(net, store, nil, settlement.NewNoOpNotifier(), 0, true, notifier)
	if _, err := svc.PayOut(context.Background(), connect.NewRequest(&payment.PayoutRequest{PaymentId: 5})); err != nil {
		t.Fatalf("PayOut() error = %v", err)
	}

	if len(notifier.manualAmlCheckRequired) != 0 {
		t.Errorf("expected no manual aml notification when auto-approve is on, got %d", len(notifier.manualAmlCheckRequired))
	}

	svc.approveAmlAfter(5, 10*time.Millisecond)

	if len(net.completed) != 1 || net.completed[0] != 5 {
		t.Errorf("expected CompleteManualAmlCheck for payment 5, got %v", net.completed)
	}
}

func TestProviderServiceImplementation_UpdatePayment(t *testing.T) {
	store := newTestStore(t)

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())

	if _, err := svc.PayOut(context.Background(), connect.NewRequest(&payment.PayoutRequest{PaymentId: 7})); err != nil {
		t.Fatalf("PayOut() error = %v", err)
	}

	p, err := store.GetByPaymentID(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetByPaymentID() error = %v", err)
	}
	if err := store.UpdateAmlDecision(context.Background(), p.ID, true, "op", ""); err != nil {
		t.Fatalf("UpdateAmlDecision() error = %v", err)
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

	p, err = store.GetByPaymentID(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetByPaymentID() error = %v", err)
	}
	if p.Status != localpayment.StatusQuoteConfirmed {
		t.Errorf("expected status %s, got %s", localpayment.StatusQuoteConfirmed, p.Status)
	}
}

func TestProviderServiceImplementation_UpdatePayment_FromCreated(t *testing.T) {
	store := newTestStore(t)

	id, err := store.Create(context.Background(), localpayment.Payment{
		PaymentClientID: "client-created",
		Role:            localpayment.RoleOFI,
		Status:          localpayment.StatusCreated,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(200)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())
	if _, err := svc.UpdatePayment(context.Background(), connect.NewRequest(&payment.UpdatePaymentRequest{
		PaymentId: 200,
		Result:    &payment.UpdatePaymentRequest_Accepted_{Accepted: &payment.UpdatePaymentRequest_Accepted{}},
	})); err != nil {
		t.Fatalf("UpdatePayment() error = %v", err)
	}

	p, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if p.Status != localpayment.StatusAccepted {
		t.Errorf("expected status %s, got %s", localpayment.StatusAccepted, p.Status)
	}
}

func TestProviderServiceImplementation_UpdatePayment_Confirmed(t *testing.T) {
	store := newTestStore(t)

	id, err := store.Create(context.Background(), localpayment.Payment{
		PaymentClientID: "client-confirmed",
		Role:            localpayment.RoleOFI,
		Status:          localpayment.StatusPayoutAccepted,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(201)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())
	if _, err := svc.UpdatePayment(context.Background(), connect.NewRequest(&payment.UpdatePaymentRequest{
		PaymentId: 201,
		Result:    &payment.UpdatePaymentRequest_Confirmed_{Confirmed: &payment.UpdatePaymentRequest_Confirmed{}},
	})); err != nil {
		t.Fatalf("UpdatePayment() error = %v", err)
	}

	p, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if p.Status != localpayment.StatusConfirmed {
		t.Errorf("expected status %s, got %s", localpayment.StatusConfirmed, p.Status)
	}
}

func TestProviderServiceImplementation_UpdatePayment_Failed(t *testing.T) {
	store := newTestStore(t)

	id, err := store.Create(context.Background(), localpayment.Payment{
		PaymentClientID: "client-failed",
		Role:            localpayment.RoleOFI,
		Status:          localpayment.StatusCreated,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(202)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())
	reason := payment.UpdatePaymentRequest_Failed_REASON_UNSPECIFIED
	if _, err := svc.UpdatePayment(context.Background(), connect.NewRequest(&payment.UpdatePaymentRequest{
		PaymentId: 202,
		Result: &payment.UpdatePaymentRequest_Failed_{Failed: &payment.UpdatePaymentRequest_Failed{
			Reason:  reason,
			Details: ptr("details"),
		}},
	})); err != nil {
		t.Fatalf("UpdatePayment() error = %v", err)
	}

	p, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if p.Status != localpayment.StatusFailed {
		t.Errorf("expected status %s, got %s", localpayment.StatusFailed, p.Status)
	}
	if p.RejectReason == "" {
		t.Error("expected reject reason to be set")
	}
}

func TestProviderServiceImplementation_UpdatePaymentUnknown(t *testing.T) {
	store := newTestStore(t)

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())

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

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())
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

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())
	resp, err := svc.AppendLedgerEntries(context.Background(), connect.NewRequest(&payment.AppendLedgerEntriesRequest{}))
	if err != nil {
		t.Fatalf("AppendLedgerEntries() error = %v", err)
	}
	if resp.Msg == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestProviderServiceImplementation_ApprovePaymentQuotes_SettlementTolerance(t *testing.T) {
	store := newTestStore(t)
	notifier := &recordingAMLNotifier{}

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 1.0, false, notifier)

	_, err := store.Create(context.Background(), localpayment.Payment{
		PaymentClientID:    "client-settlement-tolerance",
		Role:               localpayment.RoleProvider,
		Status:             localpayment.StatusPayoutRequested,
		PayoutCurrency:     "GBP",
		PayoutMethod:       "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:          ptr(uint64(300)),
		PayoutAmount:       &localpayment.Decimal{Unscaled: 1000, Exponent: 0},
		SettlementAmount:   &localpayment.Decimal{Unscaled: 950, Exponent: 0},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	resp, err := svc.ApprovePaymentQuotes(context.Background(), connect.NewRequest(&payment.ApprovePaymentQuoteRequest{
		PaymentId:        300,
		PayOutAmount:     &common.Decimal{Unscaled: 1000, Exponent: 0},
		SettlementAmount: &common.Decimal{Unscaled: 1100, Exponent: 0},
		PayOutQuoteId:    42,
	}))
	if err != nil {
		t.Fatalf("ApprovePaymentQuotes() error = %v", err)
	}
	if _, ok := resp.Msg.Result.(*payment.ApprovePaymentQuoteResponse_Rejected_); !ok {
		t.Errorf("expected Rejected, got %T", resp.Msg.Result)
	}
	if len(notifier.quoteRejected) != 1 {
		t.Errorf("expected 1 quote rejected notification, got %d", len(notifier.quoteRejected))
	}
}

func TestProviderServiceImplementation_ApprovePaymentQuotes(t *testing.T) {
	store := newTestStore(t)
	notifier := &recordingAMLNotifier{}

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 1.0, false, notifier)

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

	// Matching amount should be accepted and persisted.
	resp, err := svc.ApprovePaymentQuotes(context.Background(), connect.NewRequest(&payment.ApprovePaymentQuoteRequest{
		PaymentId:        100,
		PayOutAmount:     &common.Decimal{Unscaled: 1000, Exponent: 0},
		SettlementAmount: &common.Decimal{Unscaled: 950, Exponent: 0},
		PayOutQuoteId:    42,
	}))
	if err != nil {
		t.Fatalf("ApprovePaymentQuotes() error = %v", err)
	}
	if _, ok := resp.Msg.Result.(*payment.ApprovePaymentQuoteResponse_Accepted_); !ok {
		t.Errorf("expected Accepted, got %T", resp.Msg.Result)
	}

	p, err := store.GetByPaymentID(context.Background(), 100)
	if err != nil {
		t.Fatalf("GetByPaymentID() error = %v", err)
	}
	if p.Status != localpayment.StatusQuoteConfirmed {
		t.Errorf("expected status %s, got %s", localpayment.StatusQuoteConfirmed, p.Status)
	}
	if p.ConfirmedPayoutAmount == nil || p.ConfirmedPayoutAmount.Unscaled != 1000 {
		t.Errorf("expected confirmed payout amount 1000, got %v", p.ConfirmedPayoutAmount)
	}
	if p.ConfirmedSettlementAmount == nil || p.ConfirmedSettlementAmount.Unscaled != 950 {
		t.Errorf("expected confirmed settlement amount 950, got %v", p.ConfirmedSettlementAmount)
	}
	if p.ConfirmedQuoteID == nil || *p.ConfirmedQuoteID != 42 {
		t.Errorf("expected confirmed quote id 42, got %v", p.ConfirmedQuoteID)
	}
	if len(notifier.quoteConfirmed) != 1 {
		t.Errorf("expected 1 quote confirmed notification, got %d", len(notifier.quoteConfirmed))
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
	if len(notifier.quoteRejected) != 1 {
		t.Errorf("expected 1 quote rejected notification, got %d", len(notifier.quoteRejected))
	}
}

func ptr[T any](v T) *T {
	return &v
}

func TestProviderServiceImplementation_NewProviderServiceImplementation_NilNotifiers(t *testing.T) {
	store := newTestStore(t)
	settlementStore, err := settlement.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer func() { _ = settlementStore.Close() }()

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, settlementStore, nil, 0, false, nil)
	if svc.settlementNotifier == nil {
		t.Error("expected non-nil settlement notifier")
	}
	if svc.amlNotifier == nil {
		t.Error("expected non-nil aml notifier")
	}
}

func TestProviderServiceImplementation_PayOut_ExistingPayment(t *testing.T) {
	store := newTestStore(t)

	// Pre-create a provider payment with the network payment id.
	_, err := store.Create(context.Background(), localpayment.Payment{
		PaymentClientID: "client-existing",
		Role:            localpayment.RoleProvider,
		Status:          localpayment.StatusCreated,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(42)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())
	resp, err := svc.PayOut(context.Background(), connect.NewRequest(&payment.PayoutRequest{
		PaymentId: 42,
	}))
	if err != nil {
		t.Fatalf("PayOut() error = %v", err)
	}
	if _, ok := resp.Msg.Result.(*payment.PayoutResponse_ManualAmlCheck_); !ok {
		t.Errorf("expected ManualAmlCheck result, got %T", resp.Msg.Result)
	}
}

func TestProviderServiceImplementation_PayOut_StoreError(t *testing.T) {
	store, err := localpayment.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	_ = store.Close()

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())
	_, err = svc.PayOut(context.Background(), connect.NewRequest(&payment.PayoutRequest{
		PaymentId: 42,
	}))
	if err == nil {
		t.Fatal("expected error from closed store")
	}
}

func TestProviderServiceImplementation_MethodFromDetails(t *testing.T) {
	tests := []struct {
		name     string
		details  *common.PaymentDetails
		expected string
	}{
		{
			name:     "nil",
			details:  nil,
			expected: "",
		},
		{
			name:     "SEPA",
			details:  &common.PaymentDetails{Details: &common.PaymentDetails_Sepa_{}},
			expected: "PAYMENT_METHOD_TYPE_SEPA",
		},
		{
			name:     "SWIFT",
			details:  &common.PaymentDetails{Details: &common.PaymentDetails_Swift_{}},
			expected: "PAYMENT_METHOD_TYPE_SWIFT",
		},
		{
			name:     "FPS",
			details:  &common.PaymentDetails{Details: &common.PaymentDetails_Fps_{}},
			expected: "PAYMENT_METHOD_TYPE_FPS",
		},
		{
			name:     "ACH",
			details:  &common.PaymentDetails{Details: &common.PaymentDetails_Ach_{}},
			expected: "PAYMENT_METHOD_TYPE_ACH",
		},
		{
			name:     "unknown",
			details:  &common.PaymentDetails{Details: &common.PaymentDetails_Pix_{}},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := methodFromDetails(tt.details)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestProviderServiceImplementation_UpdateLimit_WithSettlement(t *testing.T) {
	store := newTestStore(t)
	settlementStore, err := settlement.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer func() { _ = settlementStore.Close() }()

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, settlementStore, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())
	resp, err := svc.UpdateLimit(context.Background(), connect.NewRequest(&payment.UpdateLimitRequest{
		Limits: []*payment.UpdateLimitRequest_Limit{
			{
				CounterpartId: 1,
				CreditLimit:   &common.Decimal{Unscaled: 10000, Exponent: 0},
				Reserve:       &common.Decimal{Unscaled: 1000, Exponent: 0},
				PayoutLimit:   &common.Decimal{Unscaled: 9000, Exponent: 0},
				CreditUsage:   &common.Decimal{Unscaled: 0, Exponent: 0},
			},
		},
	}))
	if err != nil {
		t.Fatalf("UpdateLimit() error = %v", err)
	}
	if resp.Msg == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestProviderServiceImplementation_AppendLedgerEntries_WithSettlement(t *testing.T) {
	store := newTestStore(t)
	settlementStore, err := settlement.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer func() { _ = settlementStore.Close() }()

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, settlementStore, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())
	resp, err := svc.AppendLedgerEntries(context.Background(), connect.NewRequest(&payment.AppendLedgerEntriesRequest{
		Transactions: []*payment.AppendLedgerEntriesRequest_Transaction{
			{
				TransactionId: 1,
				Entries: []*payment.AppendLedgerEntriesRequest_LedgerEntry{
					{
						AccountOwnerId: 1,
						AccountType:    payment.AppendLedgerEntriesRequest_ACCOUNT_TYPE_BALANCE,
						Debit:          &common.Decimal{Unscaled: 1000, Exponent: 0},
					},
				},
			},
		},
	}))
	if err != nil {
		t.Fatalf("AppendLedgerEntries() error = %v", err)
	}
	if resp.Msg == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestProviderServiceImplementation_ApprovePaymentQuotes_NotFound(t *testing.T) {
	store := newTestStore(t)
	notifier := &recordingAMLNotifier{}

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 1.0, false, notifier)

	resp, err := svc.ApprovePaymentQuotes(context.Background(), connect.NewRequest(&payment.ApprovePaymentQuoteRequest{
		PaymentId:        9999,
		PayOutAmount:     &common.Decimal{Unscaled: 1000, Exponent: 0},
		SettlementAmount: &common.Decimal{Unscaled: 950, Exponent: 0},
		PayOutQuoteId:    42,
	}))
	if err != nil {
		t.Fatalf("ApprovePaymentQuotes() error = %v", err)
	}
	if _, ok := resp.Msg.Result.(*payment.ApprovePaymentQuoteResponse_Accepted_); !ok {
		t.Errorf("expected Accepted when payment not found, got %T", resp.Msg.Result)
	}
}

func TestDecimalToFloat64_Nil(t *testing.T) {
	if decimalToFloat64(nil) != 0 {
		t.Error("expected 0 for nil decimal")
	}
}

type failingSettlementStore struct{}

func (f *failingSettlementStore) UpsertCreditLimit(_ context.Context, _ settlement.CreditLimit) error {
	return errors.New("upsert failed")
}

func (f *failingSettlementStore) AppendLedgerEntries(_ context.Context, _ uint64, _ []settlement.LedgerEntry) error {
	return errors.New("append failed")
}

func (f *failingSettlementStore) GetCreditLimits(_ context.Context) ([]settlement.CreditLimit, error) {
	return nil, nil
}

func (f *failingSettlementStore) GetCreditLimit(_ context.Context, _ int32) (*settlement.CreditLimit, error) {
	return nil, nil
}

func (f *failingSettlementStore) GetLedgerEntries(_ context.Context, _ int) ([]settlement.LedgerEntry, error) {
	return nil, nil
}

func (f *failingSettlementStore) Close() error { return nil }

var _ settlement.Store = (*failingSettlementStore)(nil)

func TestProviderServiceImplementation_UpdateLimit_SettlementError(t *testing.T) {
	store := newTestStore(t)
	settlementStore := &failingSettlementStore{}

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, settlementStore, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())
	_, err := svc.UpdateLimit(context.Background(), connect.NewRequest(&payment.UpdateLimitRequest{
		Limits: []*payment.UpdateLimitRequest_Limit{
			{
				CounterpartId: 1,
				CreditLimit:   &common.Decimal{Unscaled: 10000, Exponent: 0},
			},
		},
	}))
	if err == nil {
		t.Fatal("expected error from settlement store")
	}
}

func TestProviderServiceImplementation_AppendLedgerEntries_SettlementError(t *testing.T) {
	store := newTestStore(t)
	settlementStore := &failingSettlementStore{}

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, settlementStore, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())
	_, err := svc.AppendLedgerEntries(context.Background(), connect.NewRequest(&payment.AppendLedgerEntriesRequest{
		Transactions: []*payment.AppendLedgerEntriesRequest_Transaction{
			{
				TransactionId: 1,
				Entries: []*payment.AppendLedgerEntriesRequest_LedgerEntry{
					{
						AccountOwnerId: 1,
						AccountType:    payment.AppendLedgerEntriesRequest_ACCOUNT_TYPE_BALANCE,
						Debit:          &common.Decimal{Unscaled: 1000, Exponent: 0},
					},
				},
			},
		},
	}))
	if err == nil {
		t.Fatal("expected error from settlement store")
	}
}

func TestProviderServiceImplementation_PayOut_WithDetails(t *testing.T) {
	store := newTestStore(t)

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())
	resp, err := svc.PayOut(context.Background(), connect.NewRequest(&payment.PayoutRequest{
		PaymentId: 42,
		Amount:    &common.Decimal{Unscaled: 1000, Exponent: 0},
		PayoutDetails: &common.PaymentDetails{
			Details: &common.PaymentDetails_Swift_{Swift: &common.PaymentDetails_Swift{AccountNumber: "123"}},
		},
		TravelRuleData: &payment.PayoutRequest_TravelRuleData{},
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
	if p.PaymentDetailsJSON == "" {
		t.Error("expected payment details to be persisted")
	}
}

func TestProviderServiceImplementation_UpdatePayment_QuoteConfirmed(t *testing.T) {
	store := newTestStore(t)

	id, err := store.Create(context.Background(), localpayment.Payment{
		PaymentClientID: "client-quote-confirmed",
		Role:            localpayment.RoleOFI,
		Status:          localpayment.StatusQuoteConfirmed,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(400)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())
	if _, err := svc.UpdatePayment(context.Background(), connect.NewRequest(&payment.UpdatePaymentRequest{
		PaymentId: 400,
		Result:    &payment.UpdatePaymentRequest_Accepted_{Accepted: &payment.UpdatePaymentRequest_Accepted{}},
	})); err != nil {
		t.Fatalf("UpdatePayment() error = %v", err)
	}

	p, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if p.Status != localpayment.StatusQuoteConfirmed {
		t.Errorf("expected status %s, got %s", localpayment.StatusQuoteConfirmed, p.Status)
	}
}

func TestProviderServiceImplementation_UpdatePayment_DefaultStatus(t *testing.T) {
	store := newTestStore(t)

	id, err := store.Create(context.Background(), localpayment.Payment{
		PaymentClientID: "client-default-status",
		Role:            localpayment.RoleOFI,
		Status:          localpayment.StatusPayoutRequested,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(401)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())
	if _, err := svc.UpdatePayment(context.Background(), connect.NewRequest(&payment.UpdatePaymentRequest{
		PaymentId: 401,
		Result:    &payment.UpdatePaymentRequest_Accepted_{Accepted: &payment.UpdatePaymentRequest_Accepted{}},
	})); err != nil {
		t.Fatalf("UpdatePayment() error = %v", err)
	}

	p, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if p.Status != localpayment.StatusPayoutRequested {
		t.Errorf("expected status unchanged %s, got %s", localpayment.StatusPayoutRequested, p.Status)
	}
}

func TestProviderServiceImplementation_UpdatePayment_ManualAmlCheck(t *testing.T) {
	store := newTestStore(t)

	id, err := store.Create(context.Background(), localpayment.Payment{
		PaymentClientID: "client-manual-aml",
		Role:            localpayment.RoleOFI,
		Status:          localpayment.StatusManualAmlCheck,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(402)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())
	if _, err := svc.UpdatePayment(context.Background(), connect.NewRequest(&payment.UpdatePaymentRequest{
		PaymentId: 402,
		Result:    &payment.UpdatePaymentRequest_ManualAmlCheck_{ManualAmlCheck: &payment.UpdatePaymentRequest_ManualAmlCheck{}},
	})); err != nil {
		t.Fatalf("UpdatePayment() error = %v", err)
	}

	p, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if p.Status != localpayment.StatusManualAmlCheck {
		t.Errorf("expected status unchanged %s, got %s", localpayment.StatusManualAmlCheck, p.Status)
	}
}

func TestProviderServiceImplementation_UpdatePayment_ConfirmedWithReceipt(t *testing.T) {
	store := newTestStore(t)

	id, err := store.Create(context.Background(), localpayment.Payment{
		PaymentClientID: "client-confirmed-receipt",
		Role:            localpayment.RoleOFI,
		Status:          localpayment.StatusPayoutAccepted,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(403)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	svc := NewProviderServiceImplementation(&paymentNetwork{}, store, nil, settlement.NewNoOpNotifier(), 0, false, localpayment.NewNoOpNotifier())
	if _, err := svc.UpdatePayment(context.Background(), connect.NewRequest(&payment.UpdatePaymentRequest{
		PaymentId: 403,
		Result: &payment.UpdatePaymentRequest_Confirmed_{Confirmed: &payment.UpdatePaymentRequest_Confirmed{
			Receipt: &common.PaymentReceipt{
				Details: &common.PaymentReceipt_Sepa_{Sepa: &common.PaymentReceipt_Sepa{BankingTransactionReferenceId: ptr("receipt-403")}},
			},
		}},
	})); err != nil {
		t.Fatalf("UpdatePayment() error = %v", err)
	}

	p, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if p.Status != localpayment.StatusConfirmed {
		t.Errorf("expected status %s, got %s", localpayment.StatusConfirmed, p.Status)
	}
	if p.Receipt == "" {
		t.Error("expected receipt to be set")
	}
}

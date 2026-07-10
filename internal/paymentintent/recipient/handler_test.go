package recipient

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	sdkrecipient "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/recipient"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/recipient/recipientconnect"
	"my-provider/internal/paymentintent"
)

func newTestStore(t *testing.T) paymentintent.Store {
	t.Helper()
	store, err := paymentintent.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedBeneficiary(ctx context.Context, t *testing.T, store paymentintent.Store, id uint64) {
	t.Helper()
	_, _, err := store.GetOrCreate(ctx, paymentintent.PaymentIntent{
		ID:       id,
		Role:     paymentintent.RoleBeneficiary,
		Currency: "EUR",
		Amount:   &paymentintent.Decimal{Unscaled: 100, Exponent: 0},
		Status:   paymentintent.StatusCreated,
	})
	if err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}
}

func TestHandler_ConfirmPayIn(t *testing.T) {
	store := newTestStore(t)

	ctx := context.Background()
	seedBeneficiary(ctx, t, store, 1)

	h := NewHandler(store)
	resp, err := h.ConfirmPayIn(ctx, connect.NewRequest(&sdkrecipient.ConfirmPayInRequest{PaymentIntentId: 1}))
	if err != nil {
		t.Fatalf("ConfirmPayIn() error = %v", err)
	}
	if resp.Msg == nil {
		t.Fatal("expected non-nil response")
	}

	pi, err := store.Get(ctx, 1, paymentintent.RoleBeneficiary)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if pi.Status != paymentintent.StatusFundsReceived {
		t.Errorf("expected status %s, got %s", paymentintent.StatusFundsReceived, pi.Status)
	}
}

func TestHandler_ConfirmPayment(t *testing.T) {
	store := newTestStore(t)

	ctx := context.Background()
	seedBeneficiary(ctx, t, store, 2)
	if err := store.MarkFundsReceived(ctx, 2, paymentintent.RoleBeneficiary, timeNow(), 0); err != nil {
		t.Fatalf("MarkFundsReceived() error = %v", err)
	}

	h := NewHandler(store)
	resp, err := h.ConfirmPayment(ctx, connect.NewRequest(&sdkrecipient.ConfirmPaymentRequest{PaymentIntentId: 2}))
	if err != nil {
		t.Fatalf("ConfirmPayment() error = %v", err)
	}
	if resp.Msg == nil {
		t.Fatal("expected non-nil response")
	}

	pi, err := store.Get(ctx, 2, paymentintent.RoleBeneficiary)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if pi.Status != paymentintent.StatusConfirmed {
		t.Errorf("expected status %s, got %s", paymentintent.StatusConfirmed, pi.Status)
	}
}

func TestHandler_RejectPaymentIntent(t *testing.T) {
	store := newTestStore(t)

	ctx := context.Background()
	seedBeneficiary(ctx, t, store, 3)

	h := NewHandler(store)
	resp, err := h.RejectPaymentIntent(ctx, connect.NewRequest(&sdkrecipient.RejectPaymentIntentRequest{
		PaymentIntentId: 3,
		Reason:          "test rejection",
	}))
	if err != nil {
		t.Fatalf("RejectPaymentIntent() error = %v", err)
	}
	if resp.Msg == nil {
		t.Fatal("expected non-nil response")
	}

	pi, err := store.Get(ctx, 3, paymentintent.RoleBeneficiary)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if pi.Status != paymentintent.StatusRejected {
		t.Errorf("expected status %s, got %s", paymentintent.StatusRejected, pi.Status)
	}
}

func TestHandler_Interface(t *testing.T) {
	var _ recipientconnect.RecipientServiceHandler = (*Handler)(nil)
}

func timeNow() time.Time {
	return time.Now()
}

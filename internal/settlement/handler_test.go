package settlement

import (
	"context"
	"testing"

	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
)

type recordingNotifier struct {
	events []Event
}

func (r *recordingNotifier) Notify(_ context.Context, e Event) error {
	r.events = append(r.events, e)
	return nil
}

func TestHandler_UpdateLimitNotifies(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	rec := &recordingNotifier{}
	h := NewHandler(store, rec)

	if err := h.UpdateLimit(context.Background(), &payment.UpdateLimitRequest{
		Limits: []*payment.UpdateLimitRequest_Limit{
			{
				CounterpartId: 1,
				CreditLimit:   &common.Decimal{Unscaled: 10000, Exponent: 0},
				PayoutLimit:   &common.Decimal{Unscaled: 9000, Exponent: 0},
				CreditUsage:   &common.Decimal{Unscaled: 1000, Exponent: 0},
				Reserve:       &common.Decimal{Unscaled: 0, Exponent: 0},
			},
		},
	}); err != nil {
		t.Fatalf("UpdateLimit() error = %v", err)
	}

	if len(rec.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(rec.events))
	}
	if rec.events[0].Type != "settlement.credit" {
		t.Errorf("expected type settlement.credit, got %s", rec.events[0].Type)
	}
	if rec.events[0].CounterpartID != 1 {
		t.Errorf("expected counterpart 1, got %d", rec.events[0].CounterpartID)
	}
}

func TestHandler_AppendLedgerEntriesNotifies(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	rec := &recordingNotifier{}
	h := NewHandler(store, rec)

	if err := h.AppendLedgerEntries(context.Background(), &payment.AppendLedgerEntriesRequest{
		Transactions: []*payment.AppendLedgerEntriesRequest_Transaction{
			{
				TransactionId: 99,
				Entries: []*payment.AppendLedgerEntriesRequest_LedgerEntry{
					{
						AccountOwnerId: 1,
						AccountType:    payment.AppendLedgerEntriesRequest_ACCOUNT_TYPE_SETTLEMENT_IN,
						Credit:         &common.Decimal{Unscaled: 1000, Exponent: 0},
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("AppendLedgerEntries() error = %v", err)
	}

	if len(rec.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(rec.events))
	}
	if rec.events[0].Type != "settlement.ledger" {
		t.Errorf("expected type settlement.ledger, got %s", rec.events[0].Type)
	}
	if len(rec.events[0].LedgerEntries) != 1 {
		t.Errorf("expected 1 ledger entry, got %d", len(rec.events[0].LedgerEntries))
	}
}

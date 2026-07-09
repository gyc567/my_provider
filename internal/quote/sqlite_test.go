package quote

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*SQLiteStore, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	return store, func() {
		if err := store.Close(); err != nil {
			t.Errorf("failed to close store: %v", err)
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("failed to remove temp dir: %v", err)
		}
	}
}

func TestSQLiteStore_GetSnapshotsEmpty(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	_, err := store.GetSnapshots(context.Background(), StreamTypePayOut)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSQLiteStore_ReplaceAndGetSnapshots(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	now := time.Now().UTC()
	groups := []QuoteGroup{
		{
			Currency:      "EUR",
			PaymentMethod: "PAYMENT_METHOD_TYPE_SEPA",
			Expiration:    now.Add(time.Hour),
			Timestamp:     now,
			Bands: []Band{
				{ClientQuoteID: "eur-sepa-1k", MaxAmount: Decimal{Unscaled: 1000, Exponent: 0}, Rate: Decimal{Unscaled: 86, Exponent: -2}},
				{ClientQuoteID: "eur-sepa-5k", MaxAmount: Decimal{Unscaled: 5000, Exponent: 0}, Rate: Decimal{Unscaled: 85, Exponent: -2}},
			},
		},
	}

	if err := store.ReplaceSnapshots(context.Background(), StreamTypePayOut, groups); err != nil {
		t.Fatalf("ReplaceSnapshots() error = %v", err)
	}

	got, err := store.GetSnapshots(context.Background(), StreamTypePayOut)
	if err != nil {
		t.Fatalf("GetSnapshots() error = %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("expected 1 group, got %d", len(got))
	}
	if got[0].Currency != "EUR" {
		t.Errorf("expected EUR, got %s", got[0].Currency)
	}
	if len(got[0].Bands) != 2 {
		t.Fatalf("expected 2 bands, got %d", len(got[0].Bands))
	}
	if got[0].Bands[0].ClientQuoteID != "eur-sepa-1k" {
		t.Errorf("expected eur-sepa-1k, got %s", got[0].Bands[0].ClientQuoteID)
	}
}

func TestSQLiteStore_ReplaceSnapshotsAtomic(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	now := time.Now().UTC()
	if err := store.ReplaceSnapshots(context.Background(), StreamTypePayOut, []QuoteGroup{
		{Currency: "EUR", PaymentMethod: "PAYMENT_METHOD_TYPE_SEPA", Expiration: now.Add(time.Hour), Timestamp: now, Bands: []Band{
			{ClientQuoteID: "old", MaxAmount: Decimal{Unscaled: 1000, Exponent: 0}, Rate: Decimal{Unscaled: 86, Exponent: -2}},
		}},
	}); err != nil {
		t.Fatalf("first ReplaceSnapshots() error = %v", err)
	}

	if err := store.ReplaceSnapshots(context.Background(), StreamTypePayOut, []QuoteGroup{
		{Currency: "GBP", PaymentMethod: "PAYMENT_METHOD_TYPE_SWIFT", Expiration: now.Add(time.Hour), Timestamp: now, Bands: []Band{
			{ClientQuoteID: "new", MaxAmount: Decimal{Unscaled: 1000, Exponent: 0}, Rate: Decimal{Unscaled: 79, Exponent: -2}},
		}},
	}); err != nil {
		t.Fatalf("second ReplaceSnapshots() error = %v", err)
	}

	got, err := store.GetSnapshots(context.Background(), StreamTypePayOut)
	if err != nil {
		t.Fatalf("GetSnapshots() error = %v", err)
	}
	if len(got) != 1 || got[0].Currency != "GBP" {
		t.Errorf("expected atomic replace to GBP, got %+v", got)
	}
}

func TestSQLiteStore_PayOutPayInIsolation(t *testing.T) {
	store, cleanup := newTestStore(t)
	defer cleanup()

	now := time.Now().UTC()
	payOut := []QuoteGroup{
		{Currency: "EUR", PaymentMethod: "PAYMENT_METHOD_TYPE_SEPA", Expiration: now.Add(time.Hour), Timestamp: now, Bands: []Band{
			{ClientQuoteID: "eur-sepa-1k", MaxAmount: Decimal{Unscaled: 1000, Exponent: 0}, Rate: Decimal{Unscaled: 86, Exponent: -2}},
		}},
	}
	payIn := []QuoteGroup{
		{Currency: "EUR", PaymentMethod: "PAYMENT_METHOD_TYPE_SEPA", Expiration: now.Add(time.Hour), Timestamp: now, Bands: []Band{
			{ClientQuoteID: "eur-sepa-payin-1k", MaxAmount: Decimal{Unscaled: 1000, Exponent: 0}, Rate: Decimal{Unscaled: 88, Exponent: -2}},
		}},
	}

	if err := store.ReplaceSnapshots(context.Background(), StreamTypePayOut, payOut); err != nil {
		t.Fatalf("ReplaceSnapshots payOut error = %v", err)
	}
	if err := store.ReplaceSnapshots(context.Background(), StreamTypePayIn, payIn); err != nil {
		t.Fatalf("ReplaceSnapshots payIn error = %v", err)
	}

	gotPayOut, err := store.GetSnapshots(context.Background(), StreamTypePayOut)
	if err != nil {
		t.Fatalf("GetSnapshots payOut error = %v", err)
	}
	gotPayIn, err := store.GetSnapshots(context.Background(), StreamTypePayIn)
	if err != nil {
		t.Fatalf("GetSnapshots payIn error = %v", err)
	}

	if len(gotPayOut) != 1 || gotPayOut[0].Bands[0].Rate.Unscaled != 86 {
		t.Errorf("unexpected pay-out snapshot: %+v", gotPayOut)
	}
	if len(gotPayIn) != 1 || gotPayIn[0].Bands[0].Rate.Unscaled != 88 {
		t.Errorf("unexpected pay-in snapshot: %+v", gotPayIn)
	}
}

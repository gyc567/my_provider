package paymentintent

import (
	"context"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSQLiteStore_GetOrCreate_SameIDDifferentRoles(t *testing.T) {
	store := newTestStore(t)

	ctx := context.Background()
	provider := PaymentIntent{
		ID:       1,
		Role:     RolePayInProvider,
		Currency: "EUR",
		Amount:   &Decimal{Unscaled: 100, Exponent: 0},
		Status:   StatusCreated,
	}
	beneficiary := PaymentIntent{
		ID:       1,
		Role:     RoleBeneficiary,
		Currency: "EUR",
		Amount:   &Decimal{Unscaled: 100, Exponent: 0},
		Status:   StatusCreated,
	}

	p1, created1, err := store.GetOrCreate(ctx, provider)
	if err != nil {
		t.Fatalf("GetOrCreate(provider) error = %v", err)
	}
	if !created1 {
		t.Fatal("expected provider record to be created")
	}
	if p1.Role != RolePayInProvider {
		t.Errorf("expected role %s, got %s", RolePayInProvider, p1.Role)
	}

	b1, created2, err := store.GetOrCreate(ctx, beneficiary)
	if err != nil {
		t.Fatalf("GetOrCreate(beneficiary) error = %v", err)
	}
	if !created2 {
		t.Fatal("expected beneficiary record to be created")
	}
	if b1.Role != RoleBeneficiary {
		t.Errorf("expected role %s, got %s", RoleBeneficiary, b1.Role)
	}

	p2, err := store.Get(ctx, 1, RolePayInProvider)
	if err != nil {
		t.Fatalf("Get(provider) error = %v", err)
	}
	if p2.Role != RolePayInProvider {
		t.Errorf("expected role %s, got %s", RolePayInProvider, p2.Role)
	}
}

func TestSQLiteStore_Transitions(t *testing.T) {
	store := newTestStore(t)

	ctx := context.Background()
	pi := PaymentIntent{
		ID:       2,
		Role:     RoleBeneficiary,
		Currency: "GBP",
		Amount:   &Decimal{Unscaled: 50, Exponent: 0},
		Status:   StatusCreated,
	}
	if _, _, err := store.GetOrCreate(ctx, pi); err != nil {
		t.Fatalf("GetOrCreate() error = %v", err)
	}

	now := time.Now()
	if err := store.MarkFundsReceived(ctx, 2, RoleBeneficiary, now, 0); err != nil {
		t.Fatalf("MarkFundsReceived() error = %v", err)
	}
	if err := store.MarkConfirmed(ctx, 2, RoleBeneficiary, now); err != nil {
		t.Fatalf("MarkConfirmed() error = %v", err)
	}

	got, err := store.Get(ctx, 2, RoleBeneficiary)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Status != StatusConfirmed {
		t.Errorf("expected status %s, got %s", StatusConfirmed, got.Status)
	}
}

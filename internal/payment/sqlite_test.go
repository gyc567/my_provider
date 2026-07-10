package payment

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newSQLiteTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSQLiteStore_CreateAndGet(t *testing.T) {
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	p := Payment{
		PaymentClientID:  "client-1",
		Role:             RoleOFI,
		Status:           StatusCreated,
		PayoutCurrency:   "GBP",
		PayoutMethod:     "SWIFT",
		PayoutAmount:     &Decimal{Unscaled: 1000, Exponent: 0},
		SettlementAmount: &Decimal{Unscaled: 990, Exponent: 0},
		QuoteID:          ptr(int64(42)),
		ProviderID:       ptr(int32(7)),
	}
	id, err := store.Create(ctx, p)
	require.NoError(t, err)
	require.True(t, id > 0)

	got, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, id, got.ID)
	require.Equal(t, RoleOFI, got.Role)
	require.Equal(t, StatusCreated, got.Status)
	require.Equal(t, int64(1000), got.PayoutAmount.Unscaled)

	byClient, err := store.GetByPaymentClientID(ctx, "client-1")
	require.NoError(t, err)
	require.Equal(t, id, byClient.ID)

	_, err = store.GetByID(ctx, 9999)
	require.True(t, errors.Is(err, ErrNotFound))
}

func TestSQLiteStore_GetByPaymentID(t *testing.T) {
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	pid := uint64(12345)
	id, err := store.Create(ctx, Payment{
		PaymentID:       &pid,
		PaymentClientID: "client-by-payment-id",
		Role:            RoleProvider,
		Status:          StatusPayoutRequested,
		PayoutCurrency:  "EUR",
		PayoutMethod:    "SEPA",
	})
	require.NoError(t, err)

	got, err := store.GetByPaymentID(ctx, pid)
	require.NoError(t, err)
	require.Equal(t, id, got.ID)
	require.NotNil(t, got.PaymentID)
	require.Equal(t, pid, *got.PaymentID)

	_, err = store.GetByPaymentID(ctx, 99999)
	require.True(t, errors.Is(err, ErrNotFound))
}

func TestSQLiteStore_List(t *testing.T) {
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	roleOFI := RoleOFI
	roleProvider := RoleProvider
	statusManual := StatusManualAmlCheck

	_, err := store.Create(ctx, Payment{
		PaymentClientID: "list-ofi-1",
		Role:            RoleOFI,
		Status:          StatusCreated,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "SWIFT",
	})
	require.NoError(t, err)
	_, err = store.Create(ctx, Payment{
		PaymentClientID: "list-provider-1",
		Role:            RoleProvider,
		Status:          StatusManualAmlCheck,
		PayoutCurrency:  "EUR",
		PayoutMethod:    "SEPA",
	})
	require.NoError(t, err)
	_, err = store.Create(ctx, Payment{
		PaymentClientID: "list-provider-2",
		Role:            RoleProvider,
		Status:          StatusManualAmlCheck,
		PayoutCurrency:  "USD",
		PayoutMethod:    "ACH",
	})
	require.NoError(t, err)

	all, err := store.List(ctx, ListPaymentsFilter{})
	require.NoError(t, err)
	require.Len(t, all, 3)

	ofi, err := store.List(ctx, ListPaymentsFilter{Role: &roleOFI})
	require.NoError(t, err)
	require.Len(t, ofi, 1)

	manual, err := store.List(ctx, ListPaymentsFilter{Status: &statusManual})
	require.NoError(t, err)
	require.Len(t, manual, 2)

	filtered, err := store.List(ctx, ListPaymentsFilter{Role: &roleProvider, Status: &statusManual})
	require.NoError(t, err)
	require.Len(t, filtered, 2)

	limited, err := store.List(ctx, ListPaymentsFilter{Limit: 1})
	require.NoError(t, err)
	require.Len(t, limited, 1)
}

func TestSQLiteStore_UpdateStatus(t *testing.T) {
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	id, err := store.Create(ctx, Payment{
		PaymentClientID: "update-status",
		Role:            RoleOFI,
		Status:          StatusCreated,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "SWIFT",
	})
	require.NoError(t, err)

	require.NoError(t, store.UpdateStatus(ctx, id, StatusAccepted))
	got, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusAccepted, got.Status)

	err = store.UpdateStatus(ctx, 9999, StatusAccepted)
	require.True(t, errors.Is(err, ErrNotFound))
}

func TestSQLiteStore_UpdatePayoutRequest(t *testing.T) {
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	id, err := store.Create(ctx, Payment{
		PaymentClientID: "payout-request",
		Role:            RoleProvider,
		Status:          StatusCreated,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "SWIFT",
	})
	require.NoError(t, err)

	require.NoError(t, store.UpdatePayoutRequest(ctx, id, 100, 7))
	got, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusPayoutRequested, got.Status)
	require.NotNil(t, got.PaymentID)
	require.Equal(t, uint64(100), *got.PaymentID)
	require.NotNil(t, got.PayoutProviderID)
	require.Equal(t, uint32(7), *got.PayoutProviderID)
}

func TestSQLiteStore_UpdateManualAmlCheck(t *testing.T) {
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	id, err := store.Create(ctx, Payment{
		PaymentClientID: "manual-aml",
		Role:            RoleProvider,
		Status:          StatusPayoutRequested,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "SWIFT",
	})
	require.NoError(t, err)

	require.NoError(t, store.UpdateManualAmlCheck(ctx, id))
	got, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusManualAmlCheck, got.Status)
}

func TestSQLiteStore_UpdateAmlDecision(t *testing.T) {
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	id, err := store.Create(ctx, Payment{
		PaymentClientID: "aml-decision",
		Role:            RoleProvider,
		Status:          StatusManualAmlCheck,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "SWIFT",
	})
	require.NoError(t, err)

	require.NoError(t, store.UpdateAmlDecision(ctx, id, true, "operator-1", ""))
	got, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusAmlApproved, got.Status)
	require.Equal(t, "operator-1", got.AmlDecisionBy)
	require.NotNil(t, got.AmlDecisionAt)

	id2, err := store.Create(ctx, Payment{
		PaymentClientID: "aml-decision-reject",
		Role:            RoleProvider,
		Status:          StatusManualAmlCheck,
		PayoutCurrency:  "EUR",
		PayoutMethod:    "SEPA",
	})
	require.NoError(t, err)

	require.NoError(t, store.UpdateAmlDecision(ctx, id2, false, "operator-2", "suspicious"))
	got2, err := store.GetByID(ctx, id2)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, got2.Status)
	require.Equal(t, "suspicious", got2.RejectReason)
}

func TestSQLiteStore_UpdateQuoteConfirmed(t *testing.T) {
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	id, err := store.Create(ctx, Payment{
		PaymentClientID: "quote-confirmed",
		Role:            RoleOFI,
		Status:          StatusAmlApproved,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "SWIFT",
	})
	require.NoError(t, err)

	payout := &Decimal{Unscaled: 995, Exponent: 0}
	settlement := &Decimal{Unscaled: 985, Exponent: 0}
	require.NoError(t, store.UpdateQuoteConfirmed(ctx, id, payout, settlement, 200))

	got, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusQuoteConfirmed, got.Status)
	require.Equal(t, int64(995), got.ConfirmedPayoutAmount.Unscaled)
	require.Equal(t, int64(985), got.ConfirmedSettlementAmount.Unscaled)
	require.NotNil(t, got.ConfirmedQuoteID)
	require.Equal(t, int64(200), *got.ConfirmedQuoteID)
}

func TestSQLiteStore_UpdateConfirmed(t *testing.T) {
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	id, err := store.Create(ctx, Payment{
		PaymentClientID: "confirmed",
		Role:            RoleOFI,
		Status:          StatusPayoutAccepted,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "SWIFT",
	})
	require.NoError(t, err)

	require.NoError(t, store.UpdateConfirmed(ctx, id, "payout-xyz", "receipt-abc"))
	got, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusConfirmed, got.Status)
	require.Equal(t, "payout-xyz", got.PayoutID)
	require.Equal(t, "receipt-abc", got.Receipt)
}

func TestSQLiteStore_UpdateFailed(t *testing.T) {
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	id, err := store.Create(ctx, Payment{
		PaymentClientID: "failed",
		Role:            RoleOFI,
		Status:          StatusCreated,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "SWIFT",
	})
	require.NoError(t, err)

	require.NoError(t, store.UpdateFailed(ctx, id, "network rejected"))
	got, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, got.Status)
	require.Equal(t, "network rejected", got.RejectReason)
}

func TestSQLiteStore_UpdateFinalize(t *testing.T) {
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	id, err := store.Create(ctx, Payment{
		PaymentClientID: "finalize",
		Role:            RoleProvider,
		Status:          StatusQuoteConfirmed,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "SWIFT",
	})
	require.NoError(t, err)

	require.NoError(t, store.UpdateFinalize(ctx, id, "payout-final", "receipt-final"))
	got, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusPayoutAccepted, got.Status)
	require.Equal(t, "payout-final", got.PayoutID)
	require.Equal(t, "receipt-final", got.Receipt)
}

func TestSQLiteStore_UpdateAccepted(t *testing.T) {
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	id, err := store.Create(ctx, Payment{
		PaymentClientID: "accepted",
		Role:            RoleOFI,
		Status:          StatusCreated,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "SWIFT",
	})
	require.NoError(t, err)

	require.NoError(t, store.UpdateAccepted(ctx, id, &Decimal{Unscaled: 950, Exponent: 0}))
	got, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusAccepted, got.Status)
	require.Equal(t, int64(950), got.PayoutAmount.Unscaled)
}

func TestStatus_IsTerminal(t *testing.T) {
	require.True(t, StatusConfirmed.IsTerminal())
	require.True(t, StatusFailed.IsTerminal())
	require.False(t, StatusManualAmlCheck.IsTerminal())
	require.False(t, StatusCreated.IsTerminal())
}

func TestPayment_Validate(t *testing.T) {
	require.NoError(t, Payment{PaymentClientID: "x", PayoutCurrency: "GBP", PayoutMethod: "SWIFT"}.Validate())
	require.Error(t, Payment{PaymentClientID: "", PayoutCurrency: "GBP", PayoutMethod: "SWIFT"}.Validate())
	require.Error(t, Payment{PaymentClientID: "x", PayoutCurrency: "gbp", PayoutMethod: "SWIFT"}.Validate())
	require.Error(t, Payment{PaymentClientID: "x", PayoutCurrency: "GBPP", PayoutMethod: "SWIFT"}.Validate())
	require.Error(t, Payment{PaymentClientID: "x", PayoutCurrency: "GBP", PayoutMethod: ""}.Validate())
}

func TestJSONRaw_Marshal(t *testing.T) {
	var empty JSONRaw
	b, err := json.Marshal(empty)
	require.NoError(t, err)
	require.Equal(t, "{}", string(b))

	raw := JSONRaw(`{"a":1}`)
	b, err = json.Marshal(raw)
	require.NoError(t, err)
	require.Equal(t, `{"a":1}`, string(b))
}

func TestNewSQLiteStore_InvalidPath(t *testing.T) {
	_, err := NewSQLiteStore("/dev/null/invalid/test.db")
	require.Error(t, err)
}

func TestSQLiteStore_DuplicateClientID(t *testing.T) {
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	_, err := store.Create(ctx, Payment{PaymentClientID: "dup", Role: RoleOFI, Status: StatusCreated, PayoutCurrency: "GBP", PayoutMethod: "SWIFT"})
	require.NoError(t, err)

	_, err = store.Create(ctx, Payment{PaymentClientID: "dup", Role: RoleOFI, Status: StatusCreated, PayoutCurrency: "GBP", PayoutMethod: "SWIFT"})
	require.Error(t, err)
}

func TestSQLiteStore_CreateWithNilDecimals(t *testing.T) {
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	id, err := store.Create(ctx, Payment{
		PaymentClientID: "nil-decimals",
		Role:            RoleOFI,
		Status:          StatusCreated,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "SWIFT",
	})
	require.NoError(t, err)

	got, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	require.Nil(t, got.PayoutAmount)
	require.Nil(t, got.SettlementAmount)
}

func TestSQLiteStore_ListOrdering(t *testing.T) {
	store := newSQLiteTestStore(t)
	ctx := context.Background()

	id1, err := store.Create(ctx, Payment{PaymentClientID: "older", Role: RoleOFI, Status: StatusCreated, PayoutCurrency: "GBP", PayoutMethod: "SWIFT"})
	require.NoError(t, err)
	id2, err := store.Create(ctx, Payment{PaymentClientID: "newer", Role: RoleOFI, Status: StatusCreated, PayoutCurrency: "GBP", PayoutMethod: "SWIFT"})
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)
	require.NoError(t, store.UpdateStatus(ctx, id1, StatusAccepted))

	list, err := store.List(ctx, ListPaymentsFilter{})
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, id1, list[0].ID) // most recently updated first
	require.Equal(t, id2, list[1].ID)
}

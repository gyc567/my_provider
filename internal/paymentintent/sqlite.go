package paymentintent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteStore implements Store backed by SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens or creates the SQLite database at the given path.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database: %w", err)
	}

	db.SetMaxOpenConns(10)

	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		closeDB(db)
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}

	if err := migrate(db); err != nil {
		closeDB(db)
		return nil, fmt.Errorf("migrating database: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

func closeDB(db *sql.DB) {
	if err := db.Close(); err != nil {
		log.Printf("failed to close database: %v", err)
	}
}

func migrate(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS payment_intents (
    id INTEGER NOT NULL,
    role TEXT NOT NULL,
    currency TEXT NOT NULL,
    amount_unscaled INTEGER,
    amount_exponent INTEGER NOT NULL DEFAULT 0,
    merchant_id INTEGER,
    payment_reference TEXT,
    payment_method TEXT,
    payment_url TEXT,
    status TEXT NOT NULL,
    payout_currency TEXT,
    payout_payment_id INTEGER,
    funds_received_at TIMESTAMP,
    payout_confirmed_at TIMESTAMP,
    rejected_at TIMESTAMP,
    reject_reason TEXT,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (id, role)
);

CREATE INDEX IF NOT EXISTS idx_payment_intents_status ON payment_intents(status);
CREATE INDEX IF NOT EXISTS idx_payment_intents_role ON payment_intents(role);
`
	_, err := db.Exec(schema)
	return err
}

// GetOrCreate implements Store.
func (s *SQLiteStore) GetOrCreate(ctx context.Context, pi PaymentIntent) (*PaymentIntent, bool, error) {
	existing, err := s.Get(ctx, pi.ID, pi.Role)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}

	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO payment_intents (
			id, role, currency, amount_unscaled, amount_exponent,
			merchant_id, payment_reference, payment_method, payment_url,
			status, payout_currency, payout_payment_id,
			funds_received_at, payout_confirmed_at, rejected_at, reject_reason,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		pi.ID, string(pi.Role), pi.Currency,
		nullDecimalUnscaled(pi.Amount), nullDecimalExponent(pi.Amount),
		nullUint32(pi.MerchantID), pi.PaymentReference, pi.PaymentMethod, pi.PaymentURL,
		string(StatusCreated), pi.PayoutCurrency, nullUint64(pi.PayoutPaymentID),
		nilTime(pi.FundsReceivedAt), nilTime(pi.PayoutConfirmedAt), nilTime(pi.RejectedAt), pi.RejectReason,
		now, now,
	)
	if err != nil {
		return nil, false, fmt.Errorf("inserting payment intent: %w", err)
	}
	created, err := s.Get(ctx, pi.ID, pi.Role)
	return created, true, err
}

// Get implements Store.
func (s *SQLiteStore) Get(ctx context.Context, id uint64, role Role) (*PaymentIntent, error) {
	row := s.db.QueryRowContext(ctx, paymentIntentSelect+` WHERE id = ? AND role = ?`, id, string(role))
	return scanPaymentIntent(row)
}

// MarkFundsReceived implements Store.
func (s *SQLiteStore) MarkFundsReceived(ctx context.Context, id uint64, role Role, at time.Time, payoutProviderID uint32) error {
	return s.transition(ctx, id, role, StatusCreated, StatusFundsReceived, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE payment_intents SET status = ?, funds_received_at = ?, updated_at = ? WHERE id = ? AND role = ?
		`, string(StatusFundsReceived), at.UTC(), time.Now().UTC(), id, string(role))
		return err
	})
}

// MarkPayoutConfirmed implements Store.
func (s *SQLiteStore) MarkPayoutConfirmed(ctx context.Context, id uint64, role Role, paymentID uint64, at time.Time) error {
	return s.transition(ctx, id, role, StatusFundsReceived, StatusPayoutConfirmed, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE payment_intents SET status = ?, payout_payment_id = ?, payout_confirmed_at = ?, updated_at = ? WHERE id = ? AND role = ?
		`, string(StatusPayoutConfirmed), paymentID, at.UTC(), time.Now().UTC(), id, string(role))
		return err
	})
}

// MarkConfirmed implements Store.
func (s *SQLiteStore) MarkConfirmed(ctx context.Context, id uint64, role Role, at time.Time) error {
	return s.transition(ctx, id, role, StatusFundsReceived, StatusConfirmed, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE payment_intents SET status = ?, payout_confirmed_at = ?, updated_at = ? WHERE id = ? AND role = ?
		`, string(StatusConfirmed), at.UTC(), time.Now().UTC(), id, string(role))
		return err
	})
}

// MarkRejected implements Store.
func (s *SQLiteStore) MarkRejected(ctx context.Context, id uint64, role Role, reason string, at time.Time) error {
	pi, err := s.Get(ctx, id, role)
	if err != nil {
		return err
	}
	if pi.Status == StatusPayoutConfirmed || pi.Status == StatusConfirmed {
		return ErrInvalidTransition
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE payment_intents SET status = ?, rejected_at = ?, reject_reason = ?, updated_at = ? WHERE id = ? AND role = ?
	`, string(StatusRejected), at.UTC(), reason, time.Now().UTC(), id, string(role))
	return err
}

// Close implements Store.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) transition(ctx context.Context, id uint64, role Role, from, to Status, update func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			log.Printf("failed to rollback transaction: %v", err)
		}
	}()

	var current Status
	row := tx.QueryRowContext(ctx, `SELECT status FROM payment_intents WHERE id = ? AND role = ?`, id, string(role))
	if err := row.Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("querying status: %w", err)
	}
	if current != from {
		return ErrInvalidTransition
	}
	if err := update(tx); err != nil {
		return err
	}
	return tx.Commit()
}

const paymentIntentSelect = `
SELECT
    id, role, currency, amount_unscaled, amount_exponent,
    merchant_id, payment_reference, payment_method, payment_url,
    status, payout_currency, payout_payment_id,
    funds_received_at, payout_confirmed_at, rejected_at, reject_reason,
    created_at, updated_at
FROM payment_intents
`

func scanPaymentIntent(row *sql.Row) (*PaymentIntent, error) {
	var pi PaymentIntent
	var amountUnscaled sql.NullInt64
	var amountExponent int32
	var merchantID sql.NullInt32
	var payoutPaymentID sql.NullInt64
	var fundsReceivedAt, payoutConfirmedAt, rejectedAt sql.NullTime

	err := row.Scan(
		&pi.ID, &pi.Role, &pi.Currency, &amountUnscaled, &amountExponent,
		&merchantID, &pi.PaymentReference, &pi.PaymentMethod, &pi.PaymentURL,
		&pi.Status, &pi.PayoutCurrency, &payoutPaymentID,
		&fundsReceivedAt, &payoutConfirmedAt, &rejectedAt, &pi.RejectReason,
		&pi.CreatedAt, &pi.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scanning payment intent: %w", err)
	}

	pi.Amount = decimalFromNull(amountUnscaled, amountExponent)
	if merchantID.Valid {
		mid := uint32(merchantID.Int32)
		pi.MerchantID = mid
	}
	if payoutPaymentID.Valid {
		pid := uint64(payoutPaymentID.Int64)
		pi.PayoutPaymentID = &pid
	}
	pi.FundsReceivedAt = timePtrFromNull(fundsReceivedAt)
	pi.PayoutConfirmedAt = timePtrFromNull(payoutConfirmedAt)
	pi.RejectedAt = timePtrFromNull(rejectedAt)

	return &pi, nil
}

func decimalFromNull(unscaled sql.NullInt64, exponent int32) *Decimal {
	if !unscaled.Valid {
		return nil
	}
	return &Decimal{Unscaled: unscaled.Int64, Exponent: exponent}
}

func nullDecimalUnscaled(d *Decimal) sql.NullInt64 {
	if d == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: d.Unscaled, Valid: true}
}

func nullDecimalExponent(d *Decimal) int32 {
	if d == nil {
		return 0
	}
	return d.Exponent
}

func nullUint64(v *uint64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}

func nullUint32(v uint32) sql.NullInt32 {
	return sql.NullInt32{Int32: int32(v), Valid: true}
}

func nilTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}

func timePtrFromNull(nt sql.NullTime) *time.Time {
	if !nt.Valid {
		return nil
	}
	t := nt.Time
	return &t
}

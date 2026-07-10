package payment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
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
CREATE TABLE IF NOT EXISTS payments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    payment_id INTEGER UNIQUE,
    payment_client_id TEXT UNIQUE NOT NULL,
    role TEXT NOT NULL DEFAULT 'unknown',
    status TEXT NOT NULL,
    payout_currency TEXT NOT NULL,
    payout_method TEXT NOT NULL,
    payout_amount_unscaled INTEGER,
    payout_amount_exponent INTEGER NOT NULL DEFAULT 0,
    settlement_amount_unscaled INTEGER,
    settlement_amount_exponent INTEGER NOT NULL DEFAULT 0,
    quote_id INTEGER,
    provider_id INTEGER,
    payout_provider_id INTEGER,
    payment_details_json TEXT NOT NULL DEFAULT '{}',
    travel_rule_data_json TEXT NOT NULL DEFAULT '{}',
    payout_id TEXT,
    receipt TEXT,
    reject_reason TEXT,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_payments_payment_id ON payments(payment_id);
CREATE INDEX IF NOT EXISTS idx_payments_client_id ON payments(payment_client_id);
CREATE INDEX IF NOT EXISTS idx_payments_status ON payments(status);
`
	if _, err := db.Exec(schema); err != nil {
		return err
	}

	// Migration: role column was added after initial schema creation.
	if _, err := db.Exec(`ALTER TABLE payments ADD COLUMN role TEXT NOT NULL DEFAULT 'unknown'`); err != nil {
		// SQLite returns an error if the column already exists; ignore it.
		if !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	return nil
}

// Create implements Store.
func (s *SQLiteStore) Create(ctx context.Context, p Payment) (int64, error) {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO payments (
			payment_id, payment_client_id, role, status,
			payout_currency, payout_method,
			payout_amount_unscaled, payout_amount_exponent,
			settlement_amount_unscaled, settlement_amount_exponent,
			quote_id, provider_id, payout_provider_id,
			payment_details_json, travel_rule_data_json,
			payout_id, receipt, reject_reason,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		nullUint64(p.PaymentID), p.PaymentClientID, string(p.Role), string(p.Status),
		p.PayoutCurrency, p.PayoutMethod,
		nullDecimalUnscaled(p.PayoutAmount), nullDecimalExponent(p.PayoutAmount),
		nullDecimalUnscaled(p.SettlementAmount), nullDecimalExponent(p.SettlementAmount),
		nullInt64(p.QuoteID), nullInt32(p.ProviderID), nullUint32(p.PayoutProviderID),
		p.PaymentDetailsJSON, p.TravelRuleDataJSON,
		p.PayoutID, p.Receipt, p.RejectReason,
		now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting payment: %w", err)
	}
	return res.LastInsertId()
}

// GetByID implements Store.
func (s *SQLiteStore) GetByID(ctx context.Context, id int64) (*Payment, error) {
	row := s.db.QueryRowContext(ctx, paymentSelect+` WHERE id = ?`, id)
	return scanPayment(row)
}

// GetByPaymentClientID implements Store.
func (s *SQLiteStore) GetByPaymentClientID(ctx context.Context, clientID string) (*Payment, error) {
	row := s.db.QueryRowContext(ctx, paymentSelect+` WHERE payment_client_id = ?`, clientID)
	return scanPayment(row)
}

// GetByPaymentID implements Store.
func (s *SQLiteStore) GetByPaymentID(ctx context.Context, paymentID uint64) (*Payment, error) {
	row := s.db.QueryRowContext(ctx, paymentSelect+` WHERE payment_id = ?`, paymentID)
	return scanPayment(row)
}

// UpdateStatus implements Store.
func (s *SQLiteStore) UpdateStatus(ctx context.Context, id int64, status Status) error {
	return s.updateField(ctx, id, `status = ?`, string(status))
}

// UpdatePayoutRequest implements Store.
func (s *SQLiteStore) UpdatePayoutRequest(ctx context.Context, id int64, paymentID uint64, payoutProviderID uint32) error {
	return s.updateField(ctx, id,
		`status = ?, payment_id = ?, payout_provider_id = ?`,
		string(StatusPayoutRequested), paymentID, payoutProviderID)
}

// UpdateAccepted implements Store.
func (s *SQLiteStore) UpdateAccepted(ctx context.Context, id int64, payoutAmount *Decimal) error {
	return s.updateField(ctx, id,
		`status = ?, payout_amount_unscaled = ?, payout_amount_exponent = ?`,
		string(StatusAccepted),
		nullDecimalUnscaled(payoutAmount), nullDecimalExponent(payoutAmount))
}

// UpdateConfirmed implements Store.
func (s *SQLiteStore) UpdateConfirmed(ctx context.Context, id int64, payoutID, receipt string) error {
	return s.updateField(ctx, id,
		`status = ?, payout_id = ?, receipt = ?`,
		string(StatusConfirmed), payoutID, receipt)
}

// UpdateFailed implements Store.
func (s *SQLiteStore) UpdateFailed(ctx context.Context, id int64, reason string) error {
	return s.updateField(ctx, id,
		`status = ?, reject_reason = ?`,
		string(StatusFailed), reason)
}

// UpdateFinalize implements Store.
func (s *SQLiteStore) UpdateFinalize(ctx context.Context, id int64, payoutID, receipt string) error {
	return s.updateField(ctx, id,
		`payout_id = ?, receipt = ?`,
		payoutID, receipt)
}

func (s *SQLiteStore) updateField(ctx context.Context, id int64, setClause string, args ...any) error {
	now := time.Now().UTC()
	args = append(args, now, id)
	res, err := s.db.ExecContext(ctx,
		`UPDATE payments SET `+setClause+`, updated_at = ? WHERE id = ?`, args...)
	if err != nil {
		return fmt.Errorf("updating payment: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Close implements Store.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

const paymentSelect = `
SELECT
    id, payment_id, payment_client_id, role, status,
    payout_currency, payout_method,
    payout_amount_unscaled, payout_amount_exponent,
    settlement_amount_unscaled, settlement_amount_exponent,
    quote_id, provider_id, payout_provider_id,
    payment_details_json, travel_rule_data_json,
    payout_id, receipt, reject_reason,
    created_at, updated_at
FROM payments
`

func scanPayment(row *sql.Row) (*Payment, error) {
	var p Payment
	var paymentID sql.NullInt64
	var payoutAmountUnscaled, settlementAmountUnscaled sql.NullInt64
	var payoutAmountExponent, settlementAmountExponent int32
	var quoteID sql.NullInt64
	var providerID, payoutProviderID sql.NullInt32

	err := row.Scan(
		&p.ID, &paymentID, &p.PaymentClientID, &p.Role, &p.Status,
		&p.PayoutCurrency, &p.PayoutMethod,
		&payoutAmountUnscaled, &payoutAmountExponent,
		&settlementAmountUnscaled, &settlementAmountExponent,
		&quoteID, &providerID, &payoutProviderID,
		&p.PaymentDetailsJSON, &p.TravelRuleDataJSON,
		&p.PayoutID, &p.Receipt, &p.RejectReason,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scanning payment: %w", err)
	}

	if paymentID.Valid {
		pid := uint64(paymentID.Int64)
		p.PaymentID = &pid
	}
	p.PayoutAmount = decimalFromNull(payoutAmountUnscaled, payoutAmountExponent)
	p.SettlementAmount = decimalFromNull(settlementAmountUnscaled, settlementAmountExponent)
	if quoteID.Valid {
		qid := quoteID.Int64
		p.QuoteID = &qid
	}
	if providerID.Valid {
		pid := providerID.Int32
		p.ProviderID = &pid
	}
	if payoutProviderID.Valid {
		pid := uint32(payoutProviderID.Int32)
		p.PayoutProviderID = &pid
	}

	return &p, nil
}

func decimalFromNull(unscaled sql.NullInt64, exponent int32) *Decimal {
	if !unscaled.Valid {
		return nil
	}
	return &Decimal{Unscaled: unscaled.Int64, Exponent: exponent}
}

func nullUint64(v *uint64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}

func nullInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func nullInt32(v *int32) sql.NullInt32 {
	if v == nil {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: *v, Valid: true}
}

func nullUint32(v *uint32) sql.NullInt32 {
	if v == nil {
		return sql.NullInt32{}
	}
	return sql.NullInt32{Int32: int32(*v), Valid: true}
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

package settlement

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
CREATE TABLE IF NOT EXISTS credit_limits (
    counterpart_id INTEGER PRIMARY KEY,
    version INTEGER NOT NULL DEFAULT 0,
    credit_limit_unscaled INTEGER,
    credit_limit_exponent INTEGER NOT NULL DEFAULT 0,
    credit_usage_unscaled INTEGER,
    credit_usage_exponent INTEGER NOT NULL DEFAULT 0,
    reserve_unscaled INTEGER,
    reserve_exponent INTEGER NOT NULL DEFAULT 0,
    payout_limit_unscaled INTEGER,
    payout_limit_exponent INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS ledger_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    transaction_id INTEGER NOT NULL,
    counterpart_id INTEGER NOT NULL,
    account_type TEXT NOT NULL,
    entry_type TEXT NOT NULL,
    amount_unscaled INTEGER,
    amount_exponent INTEGER NOT NULL DEFAULT 0,
    asset TEXT NOT NULL DEFAULT 'USD',
    reference_id TEXT,
    details_json TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL,
    UNIQUE(transaction_id, counterpart_id, account_type, entry_type)
);

CREATE INDEX IF NOT EXISTS idx_ledger_transaction ON ledger_entries(transaction_id);
CREATE INDEX IF NOT EXISTS idx_ledger_created ON ledger_entries(created_at);
`
	_, err := db.Exec(schema)
	return err
}

// UpsertCreditLimit implements Store.
func (s *SQLiteStore) UpsertCreditLimit(ctx context.Context, cl CreditLimit) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO credit_limits (
			counterpart_id, version,
			credit_limit_unscaled, credit_limit_exponent,
			credit_usage_unscaled, credit_usage_exponent,
			reserve_unscaled, reserve_exponent,
			payout_limit_unscaled, payout_limit_exponent,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(counterpart_id) DO UPDATE SET
			version = excluded.version,
			credit_limit_unscaled = excluded.credit_limit_unscaled,
			credit_limit_exponent = excluded.credit_limit_exponent,
			credit_usage_unscaled = excluded.credit_usage_unscaled,
			credit_usage_exponent = excluded.credit_usage_exponent,
			reserve_unscaled = excluded.reserve_unscaled,
			reserve_exponent = excluded.reserve_exponent,
			payout_limit_unscaled = excluded.payout_limit_unscaled,
			payout_limit_exponent = excluded.payout_limit_exponent,
			updated_at = excluded.updated_at
	`,
		cl.CounterpartID, cl.Version,
		nullDecimalUnscaled(cl.CreditLimit), nullDecimalExponent(cl.CreditLimit),
		nullDecimalUnscaled(cl.CreditUsage), nullDecimalExponent(cl.CreditUsage),
		nullDecimalUnscaled(cl.Reserve), nullDecimalExponent(cl.Reserve),
		nullDecimalUnscaled(cl.PayoutLimit), nullDecimalExponent(cl.PayoutLimit),
		now,
	)
	if err != nil {
		return fmt.Errorf("upserting credit limit: %w", err)
	}
	return nil
}

// GetCreditLimits implements Store.
func (s *SQLiteStore) GetCreditLimits(ctx context.Context) ([]CreditLimit, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT counterpart_id, version,
		       credit_limit_unscaled, credit_limit_exponent,
		       credit_usage_unscaled, credit_usage_exponent,
		       reserve_unscaled, reserve_exponent,
		       payout_limit_unscaled, payout_limit_exponent,
		       updated_at
		FROM credit_limits
		ORDER BY counterpart_id
	`)
	if err != nil {
		return nil, fmt.Errorf("querying credit limits: %w", err)
	}
	defer closeRows(rows)

	var out []CreditLimit
	for rows.Next() {
		cl, err := scanCreditLimit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cl)
	}
	return out, rows.Err()
}

// GetCreditLimit implements Store.
func (s *SQLiteStore) GetCreditLimit(ctx context.Context, counterpartID int32) (*CreditLimit, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT counterpart_id, version,
		       credit_limit_unscaled, credit_limit_exponent,
		       credit_usage_unscaled, credit_usage_exponent,
		       reserve_unscaled, reserve_exponent,
		       payout_limit_unscaled, payout_limit_exponent,
		       updated_at
		FROM credit_limits
		WHERE counterpart_id = ?
	`, counterpartID)
	cl, err := scanCreditLimit(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &cl, nil
}

// AppendLedgerEntries implements Store.
func (s *SQLiteStore) AppendLedgerEntries(ctx context.Context, transactionID uint64, entries []LedgerEntry) error {
	now := time.Now().UTC()
	for _, e := range entries {
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO ledger_entries (
				transaction_id, counterpart_id, account_type, entry_type,
				amount_unscaled, amount_exponent, asset, reference_id, details_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(transaction_id, counterpart_id, account_type, entry_type) DO NOTHING
		`,
			transactionID, e.CounterpartID, e.AccountType, e.EntryType,
			nullDecimalUnscaled(e.Amount), nullDecimalExponent(e.Amount),
			e.Asset, e.ReferenceID, e.DetailsJSON, now,
		)
		if err != nil {
			return fmt.Errorf("inserting ledger entry: %w", err)
		}
	}
	return nil
}

// GetLedgerEntries implements Store.
func (s *SQLiteStore) GetLedgerEntries(ctx context.Context, limit int) ([]LedgerEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, transaction_id, counterpart_id, account_type, entry_type,
		       amount_unscaled, amount_exponent, asset, reference_id, details_json, created_at
		FROM ledger_entries
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("querying ledger entries: %w", err)
	}
	defer closeRows(rows)

	var out []LedgerEntry
	for rows.Next() {
		var e LedgerEntry
		var amountUnscaled sql.NullInt64
		var amountExponent int32
		err := rows.Scan(
			&e.ID, &e.TransactionID, &e.CounterpartID, &e.AccountType, &e.EntryType,
			&amountUnscaled, &amountExponent, &e.Asset, &e.ReferenceID, &e.DetailsJSON, &e.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning ledger entry: %w", err)
		}
		e.Amount = decimalFromNull(amountUnscaled, amountExponent)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Close implements Store.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func scanCreditLimit(row interface {
	Scan(dest ...any) error
}) (CreditLimit, error) {
	var cl CreditLimit
	var clUnscaled, cuUnscaled, rUnscaled, plUnscaled sql.NullInt64
	var clExp, cuExp, rExp, plExp int32

	err := row.Scan(
		&cl.CounterpartID, &cl.Version,
		&clUnscaled, &clExp,
		&cuUnscaled, &cuExp,
		&rUnscaled, &rExp,
		&plUnscaled, &plExp,
		&cl.UpdatedAt,
	)
	if err != nil {
		return cl, err
	}

	cl.CreditLimit = decimalFromNull(clUnscaled, clExp)
	cl.CreditUsage = decimalFromNull(cuUnscaled, cuExp)
	cl.Reserve = decimalFromNull(rUnscaled, rExp)
	cl.PayoutLimit = decimalFromNull(plUnscaled, plExp)
	return cl, nil
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

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		log.Printf("failed to close rows: %v", err)
	}
}

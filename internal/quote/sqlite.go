// Package quote provides models, validation, persistence, and publishing
// for t-0 Network quote snapshots.
package quote

import (
	"context"
	"database/sql"
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

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		log.Printf("failed to close rows: %v", err)
	}
}

func migrate(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS quote_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    stream_type TEXT NOT NULL,
    currency TEXT NOT NULL,
    payment_method TEXT NOT NULL,
    expiration TIMESTAMP NOT NULL,
    quote_timestamp TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(stream_type, currency, payment_method)
);

CREATE TABLE IF NOT EXISTS quote_bands (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    snapshot_id INTEGER NOT NULL,
    client_quote_id TEXT NOT NULL,
    max_amount_unscaled INTEGER NOT NULL,
    max_amount_exponent INTEGER NOT NULL DEFAULT 0,
    rate_unscaled INTEGER NOT NULL,
    rate_exponent INTEGER NOT NULL DEFAULT 0,
    fix_unscaled INTEGER,
    fix_exponent INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (snapshot_id) REFERENCES quote_snapshots(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_bands_snapshot ON quote_bands(snapshot_id);
`
	_, err := db.Exec(schema)
	return err
}

// GetSnapshots implements Store.
func (s *SQLiteStore) GetSnapshots(ctx context.Context, stream StreamType) ([]QuoteGroup, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, currency, payment_method, expiration, quote_timestamp
		FROM quote_snapshots
		WHERE stream_type = ?
		ORDER BY currency, payment_method
	`, string(stream))
	if err != nil {
		return nil, fmt.Errorf("querying snapshots: %w", err)
	}
	defer closeRows(rows)

	var groups []QuoteGroup
	for rows.Next() {
		var id int64
		var g QuoteGroup
		if err := rows.Scan(&id, &g.Currency, &g.PaymentMethod, &g.Expiration, &g.Timestamp); err != nil {
			return nil, fmt.Errorf("scanning snapshot: %w", err)
		}

		bands, err := s.getBands(ctx, id)
		if err != nil {
			return nil, err
		}
		g.Bands = bands
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(groups) == 0 {
		return nil, ErrNotFound
	}
	return groups, nil
}

func (s *SQLiteStore) getBands(ctx context.Context, snapshotID int64) ([]Band, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT client_quote_id, max_amount_unscaled, max_amount_exponent,
		       rate_unscaled, rate_exponent, fix_unscaled, fix_exponent
		FROM quote_bands
		WHERE snapshot_id = ?
		ORDER BY max_amount_unscaled
	`, snapshotID)
	if err != nil {
		return nil, fmt.Errorf("querying bands: %w", err)
	}
	defer closeRows(rows)

	var bands []Band
	for rows.Next() {
		var b Band
		var fixUnscaled sql.NullInt64
		var fixExponent int32
		if err := rows.Scan(
			&b.ClientQuoteID,
			&b.MaxAmount.Unscaled, &b.MaxAmount.Exponent,
			&b.Rate.Unscaled, &b.Rate.Exponent,
			&fixUnscaled, &fixExponent,
		); err != nil {
			return nil, fmt.Errorf("scanning band: %w", err)
		}
		if fixUnscaled.Valid {
			fix := Decimal{Unscaled: fixUnscaled.Int64, Exponent: fixExponent}
			b.Fix = &fix
		} else {
			b.Fix = nil
		}
		bands = append(bands, b)
	}
	return bands, rows.Err()
}

// ReplaceSnapshots implements Store.
func (s *SQLiteStore) ReplaceSnapshots(ctx context.Context, stream StreamType, groups []QuoteGroup) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			log.Printf("failed to rollback transaction: %v", err)
		}
	}()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM quote_snapshots WHERE stream_type = ?`, string(stream)); err != nil {
		return fmt.Errorf("deleting existing snapshots: %w", err)
	}

	now := time.Now().UTC()
	for _, g := range groups {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO quote_snapshots (stream_type, currency, payment_method, expiration, quote_timestamp, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, string(stream), strings.ToUpper(g.Currency), g.PaymentMethod, g.Expiration.UTC(), g.Timestamp.UTC(), now)
		if err != nil {
			return fmt.Errorf("inserting snapshot: %w", err)
		}

		snapshotID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("getting snapshot id: %w", err)
		}

		for _, b := range g.Bands {
			var fixUnscaled sql.NullInt64
			var fixExponent int32
			if b.Fix != nil {
				fixUnscaled = sql.NullInt64{Int64: b.Fix.Unscaled, Valid: true}
				fixExponent = b.Fix.Exponent
			}
			_, err := tx.ExecContext(ctx, `
				INSERT INTO quote_bands (snapshot_id, client_quote_id, max_amount_unscaled, max_amount_exponent,
				                       rate_unscaled, rate_exponent, fix_unscaled, fix_exponent)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			`, snapshotID, b.ClientQuoteID, b.MaxAmount.Unscaled, b.MaxAmount.Exponent,
				b.Rate.Unscaled, b.Rate.Exponent, fixUnscaled, fixExponent)
			if err != nil {
				return fmt.Errorf("inserting band: %w", err)
			}
		}
	}

	return tx.Commit()
}

// Close implements Store.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

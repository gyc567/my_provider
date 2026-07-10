package settlement

import (
	"context"
	"errors"
)

// Store persists credit limits and ledger entries.
type Store interface {
	// UpsertCreditLimit replaces the latest credit limit snapshot for a counterparty.
	UpsertCreditLimit(ctx context.Context, cl CreditLimit) error

	// GetCreditLimits returns all latest counterparty limit snapshots.
	GetCreditLimits(ctx context.Context) ([]CreditLimit, error)

	// GetCreditLimit returns the limit snapshot for a single counterparty.
	GetCreditLimit(ctx context.Context, counterpartID int32) (*CreditLimit, error)

	// AppendLedgerEntries idempotently inserts ledger entries for a transaction.
	AppendLedgerEntries(ctx context.Context, transactionID uint64, entries []LedgerEntry) error

	// GetLedgerEntries returns ledger entries ordered by creation time desc.
	GetLedgerEntries(ctx context.Context, limit int) ([]LedgerEntry, error)

	// Close closes the underlying storage.
	Close() error
}

// ErrNotFound is returned when a credit limit snapshot is missing.
var ErrNotFound = errors.New("credit limit not found")

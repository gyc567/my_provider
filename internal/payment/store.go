package payment

import (
	"context"
	"errors"
)

// Store persists payment records.
type Store interface {
	// Create inserts a new payment and returns its local ID.
	Create(ctx context.Context, p Payment) (int64, error)

	// GetByID returns a payment by local ID.
	GetByID(ctx context.Context, id int64) (*Payment, error)

	// GetByPaymentClientID returns a payment by client-provided idempotency key.
	GetByPaymentClientID(ctx context.Context, clientID string) (*Payment, error)

	// GetByPaymentID returns a payment by network-assigned payment ID.
	GetByPaymentID(ctx context.Context, paymentID uint64) (*Payment, error)

	// UpdateStatus transitions a payment's status and updated_at timestamp.
	UpdateStatus(ctx context.Context, id int64, status Status) error

	// UpdatePayoutRequest records that the network requested a payout.
	UpdatePayoutRequest(ctx context.Context, id int64, paymentID uint64, payoutProviderID uint32) error

	// UpdateAccepted records the network's Accepted update.
	UpdateAccepted(ctx context.Context, id int64, payoutAmount *Decimal) error

	// UpdateConfirmed records the network's Confirmed update.
	UpdateConfirmed(ctx context.Context, id int64, payoutID, receipt string) error

	// UpdateFailed records the network's Failed update.
	UpdateFailed(ctx context.Context, id int64, reason string) error

	// UpdateFinalize records the provider's FinalizePayout call.
	UpdateFinalize(ctx context.Context, id int64, payoutID, receipt string) error

	// Close closes the underlying storage.
	Close() error
}

// ErrNotFound is returned when a payment does not exist.
var ErrNotFound = errors.New("payment not found")

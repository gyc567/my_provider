package paymentintent

import (
	"context"
	"errors"
	"time"
)

// Store persists payment intent records.
type Store interface {
	// GetOrCreate returns an existing record for the (id, role) pair or creates a new CREATED one.
	// The second return value is true iff a new record was created.
	GetOrCreate(ctx context.Context, pi PaymentIntent) (*PaymentIntent, bool, error)

	// Get returns a payment intent by ID and role.
	Get(ctx context.Context, id uint64, role Role) (*PaymentIntent, error)

	// MarkFundsReceived transitions CREATED -> FUNDS_RECEIVED for the given role.
	MarkFundsReceived(ctx context.Context, id uint64, role Role, at time.Time, payoutProviderID uint32) error

	// MarkPayoutConfirmed transitions FUNDS_RECEIVED -> PAYOUT_CONFIRMED for the pay-in provider role.
	MarkPayoutConfirmed(ctx context.Context, id uint64, role Role, paymentID uint64, at time.Time) error

	// MarkConfirmed transitions FUNDS_RECEIVED -> CONFIRMED for the beneficiary role.
	MarkConfirmed(ctx context.Context, id uint64, role Role, at time.Time) error

	// MarkRejected transitions any non-terminal state -> REJECTED for the given role.
	MarkRejected(ctx context.Context, id uint64, role Role, reason string, at time.Time) error

	// Close closes the underlying storage.
	Close() error
}

// ErrNotFound is returned when a payment intent does not exist.
var ErrNotFound = errors.New("payment intent not found")

// ErrInvalidTransition is returned when a state transition is not allowed.
var ErrInvalidTransition = errors.New("invalid state transition for payment intent")

package quote

import (
	"context"
	"errors"
)

// Store persists quote snapshots.
type Store interface {
	// GetSnapshots returns all quote groups for the given stream.
	GetSnapshots(ctx context.Context, stream StreamType) ([]QuoteGroup, error)

	// ReplaceSnapshots atomically replaces all quote groups for the given stream.
	ReplaceSnapshots(ctx context.Context, stream StreamType, groups []QuoteGroup) error

	// Close closes the underlying storage.
	Close() error
}

// ErrNotFound is returned when no snapshots exist yet.
var ErrNotFound = errors.New("no quote snapshots found")

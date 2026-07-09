package quote

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// StreamType identifies the quote stream.
type StreamType string

const (
	StreamTypePayOut StreamType = "pay_out"
	StreamTypePayIn  StreamType = "pay_in"
)

func (s StreamType) Valid() bool {
	switch s {
	case StreamTypePayOut, StreamTypePayIn:
		return true
	}
	return false
}

// Validate checks that the stream type is one of the supported values.
func (s StreamType) Validate() error {
	if !s.Valid() {
		return fmt.Errorf("invalid stream type: %q", s)
	}
	return nil
}

// Decimal is a fixed-point decimal matching tzero.v1.common.Decimal.
type Decimal struct {
	Unscaled int64 `json:"unscaled"`
	Exponent int32 `json:"exponent"`
}

func (d Decimal) Float64() float64 {
	return float64(d.Unscaled) * pow10(d.Exponent)
}

func pow10(exp int32) float64 {
	result := 1.0
	if exp >= 0 {
		for i := int32(0); i < exp; i++ {
			result *= 10
		}
	} else {
		for i := int32(0); i < -exp; i++ {
			result /= 10
		}
	}
	return result
}

// Band represents a single volume tier within a quote group.
type Band struct {
	ClientQuoteID string   `json:"clientQuoteId"`
	MaxAmount     Decimal  `json:"maxAmount"`
	Rate          Decimal  `json:"rate"`
	Fix           *Decimal `json:"fix,omitempty"`
}

// QuoteGroup represents a single offer for one currency on one payment rail.
type QuoteGroup struct {
	Currency      string    `json:"currency"`
	PaymentMethod string    `json:"paymentMethod"`
	Expiration    time.Time `json:"expiration"`
	Timestamp     time.Time `json:"timestamp"`
	Bands         []Band    `json:"bands"`
}

// Validate implements the Layer 2 contract for QuoteGroup.
func (g QuoteGroup) Validate() error {
	return ValidateQuoteGroup(g)
}

// Snapshot holds all quote groups for a given stream.
type Snapshot struct {
	StreamType string       `json:"streamType"`
	Groups     []QuoteGroup `json:"groups"`
	UpdatedAt  time.Time    `json:"updatedAt"`
}

// StandardBandAmounts are the allowed max_amount values in USD.
var StandardBandAmounts = []int64{1000, 5000, 10000, 25000, 250000, 1000000}

// ValidateQuoteGroup validates a single quote group.
func ValidateQuoteGroup(g QuoteGroup) error {
	if len(g.Currency) != 3 || g.Currency != strings.ToUpper(g.Currency) {
		return fmt.Errorf("currency must be a 3-letter uppercase ISO 4217 code: %s", g.Currency)
	}
	if g.PaymentMethod == "" {
		return errors.New("paymentMethod is required")
	}
	if g.Expiration.IsZero() {
		return errors.New("expiration is required")
	}
	if g.Timestamp.IsZero() {
		return errors.New("timestamp is required")
	}
	if len(g.Bands) == 0 {
		return errors.New("at least one band is required")
	}

	seenMax := make(map[int64]struct{})
	seenIDs := make(map[string]struct{})
	for _, b := range g.Bands {
		if b.ClientQuoteID == "" {
			return errors.New("clientQuoteId is required for every band")
		}
		if len(b.ClientQuoteID) > 64 {
			return fmt.Errorf("clientQuoteId must be <= 64 characters: %s", b.ClientQuoteID)
		}
		if _, ok := seenIDs[b.ClientQuoteID]; ok {
			return fmt.Errorf("duplicate clientQuoteId: %s", b.ClientQuoteID)
		}
		seenIDs[b.ClientQuoteID] = struct{}{}

		if !isStandardBand(b.MaxAmount.Unscaled) {
			return fmt.Errorf("maxAmount %d is not a standard band amount", b.MaxAmount.Unscaled)
		}
		if _, ok := seenMax[b.MaxAmount.Unscaled]; ok {
			return fmt.Errorf("duplicate maxAmount %d within group", b.MaxAmount.Unscaled)
		}
		seenMax[b.MaxAmount.Unscaled] = struct{}{}
	}

	return nil
}

func isStandardBand(amount int64) bool {
	for _, a := range StandardBandAmounts {
		if a == amount {
			return true
		}
	}
	return false
}

// ValidateSnapshot validates all groups in a snapshot.
func ValidateSnapshot(groups []QuoteGroup) error {
	seen := make(map[string]struct{})
	for _, g := range groups {
		if err := g.Validate(); err != nil {
			return err
		}
		key := fmt.Sprintf("%s:%s", g.Currency, g.PaymentMethod)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate quote group for %s", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

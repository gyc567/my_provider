// Package payment implements the payout payment lifecycle for the t-0 Network.
package payment

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Status is the payment lifecycle state.
type Status string

const (
	StatusCreated         Status = "CREATED"
	StatusAccepted        Status = "ACCEPTED"
	StatusPayoutRequested Status = "PAYOUT_REQUESTED"
	StatusPayoutAccepted  Status = "PAYOUT_ACCEPTED"
	StatusConfirmed       Status = "CONFIRMED"
	StatusFailed          Status = "FAILED"
)

// Role identifies which side of the payout flow the record belongs to.
type Role string

const (
	RoleOFI      Role = "ofi"
	RoleProvider Role = "provider"
)

// Decimal mirrors tzero.v1.common.Decimal for JSON and persistence.
type Decimal struct {
	Unscaled int64 `json:"unscaled"`
	Exponent int32 `json:"exponent"`
}

// QuoteID identifies a quote within a provider.
type QuoteID struct {
	QuoteID    int64 `json:"quoteId"`
	ProviderID int32 `json:"providerId"`
}

// Payment is the domain object for a payout payment.
type Payment struct {
	ID                 int64
	PaymentID          *uint64
	PaymentClientID    string
	Role               Role
	Status             Status
	PayoutCurrency     string
	PayoutMethod       string
	PayoutAmount       *Decimal
	SettlementAmount   *Decimal
	QuoteID            *int64
	ProviderID         *int32
	PayoutProviderID   *uint32
	PaymentDetailsJSON string
	TravelRuleDataJSON string
	PayoutID           string
	Receipt            string
	RejectReason       string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Validate implements the Layer 2 contract check.
func (p Payment) Validate() error {
	if p.PaymentClientID == "" {
		return errors.New("paymentClientId is required")
	}
	if len(p.PayoutCurrency) != 3 || p.PayoutCurrency != strings.ToUpper(p.PayoutCurrency) {
		return fmt.Errorf("currency must be ISO 4217 uppercase: %s", p.PayoutCurrency)
	}
	if p.PayoutMethod == "" {
		return errors.New("payoutMethod is required")
	}
	return nil
}

// CreateRequest is the REST payload for POST /api/v1/payments.
type CreateRequest struct {
	PaymentClientID string          `json:"paymentClientId" validate:"required"`
	Amount          Decimal         `json:"amount" validate:"required"`
	AmountType      string          `json:"amountType" validate:"required,oneof=pay_out settlement"`
	Currency        string          `json:"currency" validate:"required,len=3,uppercase"`
	PaymentMethod   string          `json:"paymentMethod" validate:"required"`
	PaymentDetails  JSONRaw         `json:"paymentDetails,omitempty"`
	QuoteID         *QuoteID        `json:"quoteId,omitempty"`
	TravelRuleData  JSONRaw         `json:"travelRuleData,omitempty"`
	Purpose         string          `json:"purpose,omitempty"`
}

// FinalizeRequest is the REST payload for POST /api/v1/payments/{id}/finalize.
type FinalizeRequest struct {
	Success      bool   `json:"success"`
	PayoutID     string `json:"payoutId,omitempty"`
	Receipt      string `json:"receipt,omitempty"`
	RejectReason string `json:"rejectReason,omitempty"`
}

// JSONRaw is a thin wrapper so empty raw messages marshal as {} instead of null.
type JSONRaw []byte

func (j JSONRaw) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("{}"), nil
	}
	return j, nil
}

func (j *JSONRaw) UnmarshalJSON(data []byte) error {
	*j = append((*j)[:0], data...)
	return nil
}

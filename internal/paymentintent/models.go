// Package paymentintent provides shared models for Phase 3 payment intent flows.
package paymentintent

import (
	"errors"
	"time"
)

// Role identifies which side of the payment intent flow a record belongs to.
type Role string

const (
	RolePayInProvider Role = "pay_in_provider"
	RoleBeneficiary   Role = "beneficiary"
)

// Status is the payment intent lifecycle state.
type Status string

const (
	StatusCreated         Status = "CREATED"
	StatusFundsReceived   Status = "FUNDS_RECEIVED"
	StatusPayoutConfirmed Status = "PAYOUT_CONFIRMED"
	StatusConfirmed       Status = "CONFIRMED"
	StatusRejected        Status = "REJECTED"
)

// Decimal mirrors tzero.v1.common.Decimal.
type Decimal struct {
	Unscaled int64 `json:"unscaled"`
	Exponent int32 `json:"exponent"`
}

// PaymentIntent is the domain object for both 3A and 3B roles.
type PaymentIntent struct {
	ID                uint64     `json:"id"`
	Role              Role       `json:"role"`
	Currency          string     `json:"currency"`
	Amount            *Decimal   `json:"amount"`
	MerchantID        uint32     `json:"merchantId"`
	PaymentReference  string     `json:"paymentReference"`
	PaymentMethod     string     `json:"paymentMethod"`
	PaymentURL        string     `json:"paymentUrl"`
	Status            Status     `json:"status"`
	PayoutCurrency    string     `json:"payoutCurrency"`
	PayoutPaymentID   *uint64    `json:"payoutPaymentId"`
	FundsReceivedAt   *time.Time `json:"fundsReceivedAt"`
	PayoutConfirmedAt *time.Time `json:"payoutConfirmedAt"`
	RejectedAt        *time.Time `json:"rejectedAt"`
	RejectReason      string     `json:"rejectReason"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

// Validate checks required fields.
func (p PaymentIntent) Validate() error {
	if p.ID == 0 {
		return errors.New("id is required")
	}
	if p.Role == "" {
		return errors.New("role is required")
	}
	if p.Currency == "" {
		return errors.New("currency is required")
	}
	return nil
}

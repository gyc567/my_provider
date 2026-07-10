// Package settlement persists credit limit and ledger notifications from the t-0 Network.
package settlement

import (
	"errors"
	"time"
)

// Decimal mirrors tzero.v1.common.Decimal.
type Decimal struct {
	Unscaled int64 `json:"unscaled"`
	Exponent int32 `json:"exponent"`
}

// CreditLimit is a snapshot of limits for one counterparty.
type CreditLimit struct {
	CounterpartID int32     `json:"counterpartId"`
	Version       int64     `json:"version"`
	PayoutLimit   *Decimal  `json:"payoutLimit"`
	CreditLimit   *Decimal  `json:"creditLimit"`
	CreditUsage   *Decimal  `json:"creditUsage"`
	Reserve       *Decimal  `json:"reserve"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Validate checks required fields.
func (c CreditLimit) Validate() error {
	if c.CounterpartID == 0 {
		return errors.New("counterpartId is required")
	}
	return nil
}

// LedgerEntry is one side of a ledger transaction.
type LedgerEntry struct {
	ID             int64     `json:"id"`
	TransactionID  uint64    `json:"transactionId"`
	CounterpartID  uint32    `json:"counterpartId"`
	AccountType    string    `json:"accountType"`
	EntryType      string    `json:"entryType"` // DEBIT or CREDIT
	Amount         *Decimal  `json:"amount"`
	Asset          string    `json:"asset"`
	ReferenceID    string    `json:"referenceId"`
	DetailsJSON    string    `json:"detailsJson"`
	CreatedAt      time.Time `json:"createdAt"`
}

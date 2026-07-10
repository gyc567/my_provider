package settlement

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
)

// Handler processes Network callbacks for limits and ledger entries.
type Handler struct {
	store    Store
	notifier Notifier
}

// NewHandler creates a new settlement callback handler.
func NewHandler(store Store, notifier Notifier) *Handler {
	if notifier == nil {
		notifier = NewNoOpNotifier()
	}
	return &Handler{store: store, notifier: notifier}
}

// UpdateLimit persists a credit limit update notification.
func (h *Handler) UpdateLimit(ctx context.Context, req *payment.UpdateLimitRequest) error {
	for _, l := range req.Limits {
		cl := CreditLimit{
			CounterpartID: l.CounterpartId,
			Version:       l.Version,
			PayoutLimit:   fromCommonDecimal(l.PayoutLimit),
			CreditLimit:   fromCommonDecimal(l.CreditLimit),
			CreditUsage:   fromCommonDecimal(l.CreditUsage),
			Reserve:       fromCommonDecimal(l.Reserve),
		}
		if err := cl.Validate(); err != nil {
			log.Printf("UpdateLimit: invalid limit for counterpart_id=%d: %v", l.CounterpartId, err)
			continue
		}
		if err := h.store.UpsertCreditLimit(ctx, cl); err != nil {
			log.Printf("UpdateLimit: failed to persist counterpart_id=%d: %v", l.CounterpartId, err)
			return err
		}
		if err := h.notifier.Notify(ctx, Event{
			Type:          "settlement.credit",
			CounterpartID: uint64(cl.CounterpartID),
			CreditLimit:   cl.CreditLimit,
			PayoutLimit:   cl.PayoutLimit,
			CreditUsage:   cl.CreditUsage,
			Reserve:       cl.Reserve,
		}); err != nil {
			log.Printf("UpdateLimit: webhook failed for counterpart_id=%d: %v", cl.CounterpartID, err)
		}
	}
	return nil
}

// AppendLedgerEntries persists ledger entry notifications.
func (h *Handler) AppendLedgerEntries(ctx context.Context, req *payment.AppendLedgerEntriesRequest) error {
	for _, tx := range req.Transactions {
		entries := make([]LedgerEntry, 0, len(tx.Entries))
		for _, e := range tx.Entries {
			entry := LedgerEntry{
				TransactionID: tx.TransactionId,
				CounterpartID: e.AccountOwnerId,
				AccountType:   e.AccountType.String(),
				DetailsJSON:   transactionDetailsJSON(tx),
			}
			if e.Debit != nil && (e.Debit.Unscaled != 0 || e.Debit.Exponent != 0) {
				entry.EntryType = "DEBIT"
				entry.Amount = fromCommonDecimal(e.Debit)
			} else if e.Credit != nil && (e.Credit.Unscaled != 0 || e.Credit.Exponent != 0) {
				entry.EntryType = "CREDIT"
				entry.Amount = fromCommonDecimal(e.Credit)
			} else {
				continue
			}
			entry.ReferenceID = transactionReferenceID(tx)
			entries = append(entries, entry)
		}
		if err := h.store.AppendLedgerEntries(ctx, tx.TransactionId, entries); err != nil {
			return err
		}
		if len(entries) > 0 {
			if err := h.notifier.Notify(ctx, Event{
				Type:          "settlement.ledger",
				LedgerEntries: entries,
			}); err != nil {
				log.Printf("AppendLedgerEntries: webhook failed for transaction_id=%d: %v", tx.TransactionId, err)
			}
		}
	}
	return nil
}

func fromCommonDecimal(d *common.Decimal) *Decimal {
	if d == nil {
		return nil
	}
	return &Decimal{Unscaled: d.Unscaled, Exponent: d.Exponent}
}

func transactionDetailsJSON(tx *payment.AppendLedgerEntriesRequest_Transaction) string {
	if tx == nil || tx.TransactionDetails == nil {
		return "{}"
	}
	b, err := json.Marshal(map[string]string{
		"type": fmt.Sprintf("%T", tx.TransactionDetails),
	})
	if err != nil {
		return "{}"
	}
	return string(b)
}

func transactionReferenceID(tx *payment.AppendLedgerEntriesRequest_Transaction) string {
	if tx == nil || tx.TransactionDetails == nil {
		return ""
	}
	switch d := tx.TransactionDetails.(type) {
	case *payment.AppendLedgerEntriesRequest_Transaction_Payout_:
		if d.Payout != nil {
			return fmt.Sprintf("payment:%d", d.Payout.PaymentId)
		}
	case *payment.AppendLedgerEntriesRequest_Transaction_ProviderSettlement_:
		if d.ProviderSettlement != nil {
			return fmt.Sprintf("settlement:%d", d.ProviderSettlement.SettlementId)
		}
	}
	return ""
}

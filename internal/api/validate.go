package api

import (
	"fmt"
	"strconv"
	"strings"
)

// MaxGroups is the maximum number of currency/method groups per request.
// One HTTP call carries the full pay-out snapshot.
const MaxGroups = 50

// MaxBandsPerGroup bounds the bands per (currency, payment_method) group.
const MaxBandsPerGroup = 20

// allowedMaxAmounts is the protocol-mandated whitelist of USD max-amount
// values that sandbox will accept. Any other value triggers an
// "unsupported band" rejection upstream.
var allowedMaxAmounts = map[string]struct{}{
	"1000":     {},
	"5000":     {},
	"10000":    {},
	"25000":    {},
	"250000":   {},
	"1000000":  {},
}

// allowedCurrencies is the business whitelist of ISO 4217 currency codes
// we accept at the HTTP layer. Protocol itself accepts any 3-letter code,
// but business policy restricts it.
var allowedCurrencies = map[string]struct{}{
	"EUR": {}, "GBP": {}, "BRL": {}, "USD": {}, "CAD": {}, "AUD": {},
	"JPY": {}, "INR": {}, "MXN": {}, "CHF": {}, "SEK": {}, "NOK": {},
	"DKK": {}, "SGD": {}, "HKD": {}, "NZD": {}, "KRW": {}, "CNY": {},
}

// allowedPaymentMethods mirrors common.PaymentMethodType enum values
// MINUS PAYMENT_METHOD_TYPE_UNSPECIFIED (which we reject).
var allowedPaymentMethods = map[string]struct{}{
	"SEPA":                     {},
	"SWIFT":                    {},
	"ACH":                      {},
	"WIRE":                     {},
	"FPS":                      {},
	"G_CASH":                   {},
	"INDIAN_BANK_TRANSFER":     {},
	"PESONET":                  {},
	"INSTAPAY":                 {},
	"PAKISTAN_BANK_TRANSFER":   {},
	"PAKISTAN_MOBILE_WALLET":   {},
	"PIX":                      {},
	"AFRICAN_MOBILE_MONEY":     {},
	"CNAPS":                    {},
	"NIP":                      {},
	"M_PESA":                   {}, // deprecated upstream but accepted
}

// ValidationError is returned by (*UpdatePayOutRequest).Validate. It carries
// the HTTP-mappable code, the field that failed, and a human-readable
// detail string.
type ValidationError struct {
	HTTPStatus int
	Code       string
	Field      string
	Detail     string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s (field=%s)", e.Code, e.Detail, e.Field)
}

// Validate enforces every business rule on a request. On the first failure,
// it returns a *ValidationError describing the HTTP code, machine code,
// failing field, and human detail. Order matters — fields are checked in
// a stable order to make error messages predictable.
func (r *UpdatePayOutRequest) Validate() *ValidationError {
	if len(r.Groups) == 0 {
		return &ValidationError{400, "invalid_request", "groups", "groups is required and must be non-empty"}
	}
	if len(r.Groups) > MaxGroups {
		return &ValidationError{400, "invalid_request", "groups",
			fmt.Sprintf("groups count %d exceeds max %d", len(r.Groups), MaxGroups)}
	}
	for gi := range r.Groups {
		g := &r.Groups[gi]
		if !validCurrency(g.Currency) {
			return &ValidationError{400, "invalid_currency", "currency",
				fmt.Sprintf("currency=%q must be 3-letter ISO 4217 uppercase and in business whitelist", g.Currency)}
		}
		if !validPaymentMethod(g.PaymentMethod) {
			return &ValidationError{400, "invalid_payment_method", "payment_method",
				fmt.Sprintf("payment_method=%q is not supported (UNSPECIFIED is rejected)", g.PaymentMethod)}
		}
		if g.ExpirationSeconds < 5 || g.ExpirationSeconds > 300 {
			return &ValidationError{400, "invalid_expiration", "expiration_seconds",
				fmt.Sprintf("expiration_seconds=%d must be in [5, 300]", g.ExpirationSeconds)}
		}
		if len(g.Bands) == 0 {
			return &ValidationError{400, "invalid_request", "bands", "bands must be non-empty"}
		}
		if len(g.Bands) > MaxBandsPerGroup {
			return &ValidationError{400, "invalid_request", "bands",
				fmt.Sprintf("bands count %d exceeds max %d per group", len(g.Bands), MaxBandsPerGroup)}
		}
		seen := make(map[string]struct{}, len(g.Bands))
		for bi := range g.Bands {
			b := &g.Bands[bi]
			if len(b.ClientQuoteID) == 0 || len(b.ClientQuoteID) > 64 {
				return &ValidationError{400, "invalid_client_quote_id", "client_quote_id",
					fmt.Sprintf("client_quote_id length=%d must be in [1,64]", len(b.ClientQuoteID))}
			}
			if _, dup := seen[b.ClientQuoteID]; dup {
				return &ValidationError{400, "duplicate_client_quote_id", "client_quote_id",
					fmt.Sprintf("client_quote_id=%q appears twice in groups[%d].bands", b.ClientQuoteID, gi)}
			}
			seen[b.ClientQuoteID] = struct{}{}

			if !validMaxAmount(b.MaxAmountUSD) {
				return &ValidationError{400, "unsupported_band", "max_amount_usd",
					fmt.Sprintf("max_amount=%s not in [1000,5000,10000,25000,250000,1000000]", b.MaxAmountUSD)}
			}
			if _, err := ParseDecimal(b.Rate); err != nil {
				return &ValidationError{400, "invalid_rate", "rate",
					fmt.Sprintf("rate=%q: %s", b.Rate, err.Error())}
			}
		}
	}
	return nil
}

func validCurrency(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	_, ok := allowedCurrencies[s]
	return ok
}

func validPaymentMethod(s string) bool {
	if s == "" {
		return false
	}
	_, ok := allowedPaymentMethods[s]
	return ok
}

func validMaxAmount(s string) bool {
	if s == "" {
		return false
	}
	// Reject anything non-integer (e.g. "1000.5", "abc", "-1").
	if _, err := strconv.ParseInt(s, 10, 64); err != nil {
		return false
	}
	_, ok := allowedMaxAmounts[s]
	return ok
}

// canonicalString returns a stable, deterministic JSON-ish string for a
// request, used for Idempotency-Key body hashing. We hand-build the format
// rather than canonical-JSON-marshaling to keep this package zero-dep.
//
// Format: groups separated by '|', each group as
//
//	"cur=...&pm=...&exp=...&bands=id1:amt1:rate1,id2:amt2:rate2,..."
//
// This is a low-collision hash space; we SHA-256 it before storing.
func (r *UpdatePayOutRequest) canonicalString() string {
	var b strings.Builder
	for i, g := range r.Groups {
		if i > 0 {
			b.WriteByte('|')
		}
		fmt.Fprintf(&b, "cur=%s&pm=%s&exp=%d&bands=", g.Currency, g.PaymentMethod, g.ExpirationSeconds)
		for j, band := range g.Bands {
			if j > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%s:%s:%s", band.ClientQuoteID, band.MaxAmountUSD, band.Rate)
		}
	}
	return b.String()
}

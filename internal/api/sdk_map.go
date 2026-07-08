package api

import (
	"time"

	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// paymentMethodFromString maps our HTTP-layer enum string to the SDK
// common.PaymentMethodType enum value. Returns 0 for unknown (caller must
// have already validated).
func paymentMethodFromString(s string) common.PaymentMethodType {
	switch s {
	case "SEPA":
		return common.PaymentMethodType_PAYMENT_METHOD_TYPE_SEPA
	case "SWIFT":
		return common.PaymentMethodType_PAYMENT_METHOD_TYPE_SWIFT
	case "ACH":
		return common.PaymentMethodType_PAYMENT_METHOD_TYPE_ACH
	case "WIRE":
		return common.PaymentMethodType_PAYMENT_METHOD_TYPE_WIRE
	case "FPS":
		return common.PaymentMethodType_PAYMENT_METHOD_TYPE_FPS
	case "G_CASH":
		return common.PaymentMethodType_PAYMENT_METHOD_TYPE_G_CASH
	case "INDIAN_BANK_TRANSFER":
		return common.PaymentMethodType_PAYMENT_METHOD_TYPE_INDIAN_BANK_TRANSFER
	case "PESONET":
		return common.PaymentMethodType_PAYMENT_METHOD_TYPE_PESONET
	case "INSTAPAY":
		return common.PaymentMethodType_PAYMENT_METHOD_TYPE_INSTAPAY
	case "PAKISTAN_BANK_TRANSFER":
		return common.PaymentMethodType_PAYMENT_METHOD_TYPE_PAKISTAN_BANK_TRANSFER
	case "PAKISTAN_MOBILE_WALLET":
		return common.PaymentMethodType_PAYMENT_METHOD_TYPE_PAKISTAN_MOBILE_WALLET
	case "PIX":
		return common.PaymentMethodType_PAYMENT_METHOD_TYPE_PIX
	case "AFRICAN_MOBILE_MONEY":
		return common.PaymentMethodType_PAYMENT_METHOD_TYPE_AFRICAN_MOBILE_MONEY
	case "CNAPS":
		return common.PaymentMethodType_PAYMENT_METHOD_TYPE_CNAPS
	case "NIP":
		return common.PaymentMethodType_PAYMENT_METHOD_TYPE_NIP
	case "M_PESA":
		return common.PaymentMethodType_PAYMENT_METHOD_TYPE_M_PESA
	default:
		return common.PaymentMethodType_PAYMENT_METHOD_TYPE_UNSPECIFIED
	}
}

// ToSDKRequest builds a payment.UpdateQuoteRequest from our HTTP DTO.
// Timestamp is server-clock-injected (not client-supplied) to defend
// against client clock drift.
//
// IMPORTANT: this is called AFTER Validate succeeded, so we skip
// defensive nil checks and assume fields are well-formed.
func (r *UpdatePayOutRequest) ToSDKRequest() *payment.UpdateQuoteRequest {
	now := timestamppb.Now()
	quotes := make([]*payment.UpdateQuoteRequest_Quote, 0, len(r.Groups))
	for i := range r.Groups {
		g := &r.Groups[i]
		expiration := timestamppb.New(now.AsTime().Add(timeSecondsToDuration(g.ExpirationSeconds)))
		bands := make([]*payment.UpdateQuoteRequest_Quote_Band, 0, len(g.Bands))
		for j := range g.Bands {
			b := &g.Bands[j]
			maxAmt, _ := ParseDecimal(b.MaxAmountUSD) // guaranteed valid by Validate
			maxAmtPtr := scaleToInt(&maxAmt)          // canonical integer form
			rate, _ := ParseDecimal(b.Rate)
			ratePtr := &rate
			bands = append(bands, &payment.UpdateQuoteRequest_Quote_Band{
				ClientQuoteId: b.ClientQuoteID,
				MaxAmount:     maxAmtPtr,
				Rate:          ratePtr,
			})
		}
		quotes = append(quotes, &payment.UpdateQuoteRequest_Quote{
			Currency:      g.Currency,
			QuoteType:     payment.QuoteType_QUOTE_TYPE_REALTIME,
			PaymentMethod: paymentMethodFromString(g.PaymentMethod),
			Expiration:    expiration,
			Timestamp:     now,
			Bands:         bands,
		})
	}
	return &payment.UpdateQuoteRequest{
		PayOut: quotes,
		PayIn:  nil,
	}
}

// scaleToInt converts a Decimal whose value is exactly representable as
// an integer (e.g. {86, -2} = 0.86 is NOT; {1000, 0} = 1000 IS) to the
// canonical integer form {Unscaled, 0}. The protocol's max_amount band
// value MUST be one of {1000, 5000, 10000, 25000, 250000, 1000000} which
// are all integers, so this is always safe to call on a validated
// max_amount.
//
// ParseDecimal trims trailing zeros, so "1000" parses to {1, 3} (1 * 10^3 = 1000).
// "5000" parses to {5, 3}. We need to scale these up to {1000, 0} / {5000, 0}.
// We achieve this by multiplying Unscaled by 10^(-Exponent) when Exponent > 0.
func scaleToInt(d *common.Decimal) *common.Decimal {
	if d.Exponent == 0 {
		return d
	}
	if d.Exponent > 0 {
		// d.Unscaled * 10^d.Exponent — fits in int64 for the whitelist values.
		scale := int64(1)
		for i := int32(0); i < d.Exponent; i++ {
			scale *= 10
		}
		return &common.Decimal{Unscaled: d.Unscaled * scale, Exponent: 0}
	}
	// Exponent < 0 — d.Unscaled / 10^|Exponent|. For max_amount in the
	// whitelist, this is always an integer with no remainder (e.g. {86, -2}
	// cannot appear because no whitelist value has a fractional part).
	divisor := int64(1)
	for i := int32(0); i < -d.Exponent; i++ {
		divisor *= 10
	}
	if d.Unscaled%divisor != 0 {
		// Should be impossible for whitelist values; fall back to as-is.
		return d
	}
	return &common.Decimal{Unscaled: d.Unscaled / divisor, Exponent: 0}
}

// timeSecondsToDuration converts int32 seconds to time.Duration.
func timeSecondsToDuration(s int32) time.Duration {
	return time.Duration(s) * time.Second
}
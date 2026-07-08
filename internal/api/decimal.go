package api

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
)

// MaxDecimalPrecision is the maximum number of fractional digits we accept
// when parsing a decimal string. The protocol does not enforce a limit, but
// 8 digits is more than enough for fiat FX (sub-pip precision) and prevents
// pathological inputs from inflating unscaled values.
const MaxDecimalPrecision = 8

// ErrInvalidDecimal is returned by ParseDecimal when the input cannot be
// parsed as a strictly positive decimal with at most MaxDecimalPrecision
// fractional digits.
var ErrInvalidDecimal = errors.New("invalid decimal")

// ParseDecimal parses a string like "0.86", "1000", ".5", "0.8600" into a
// common.Decimal with canonical (trailing-zero-stripped) Unscaled/Exponent.
//
// The input must:
//   - be non-empty (after trimming whitespace)
//   - contain only digits, at most one '.' and no other characters
//   - represent a strictly positive value (> 0)
//   - have at most MaxDecimalPrecision fractional digits
//
// Returns ErrInvalidDecimal wrapping a descriptive message on any failure.
func ParseDecimal(s string) (common.Decimal, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return common.Decimal{}, fmt.Errorf("%w: empty string", ErrInvalidDecimal)
	}
	if strings.ContainsAny(s, "+eE") {
		return common.Decimal{}, fmt.Errorf("%w: scientific notation not allowed: %q", ErrInvalidDecimal, s)
	}

	// Use big.Rat for arbitrary precision parsing, then quantize to
	// MaxDecimalPrecision fractional digits.
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return common.Decimal{}, fmt.Errorf("%w: cannot parse %q", ErrInvalidDecimal, s)
	}
	if r.Sign() <= 0 {
		return common.Decimal{}, fmt.Errorf("%w: must be > 0, got %q", ErrInvalidDecimal, s)
	}

	// r is positive rational. Multiply by 10^MaxDecimalPrecision to extract
	// the unscaled integer. The result must be an integer (no remainder),
	// otherwise the input had more than MaxDecimalPrecision fractional
	// digits.
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(MaxDecimalPrecision), nil)
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(scale))
	if !scaled.IsInt() {
		return common.Decimal{}, fmt.Errorf("%w: more than %d fractional digits in %q",
			ErrInvalidDecimal, MaxDecimalPrecision, s)
	}
	unscaledBig := scaled.Num()

	// Trim trailing zeros from the unscaled value to produce a canonical
	// (Unscaled, Exponent) pair, then adjust Exponent accordingly.
	unscaled := new(big.Int).Set(unscaledBig)
	exponent := int32(-MaxDecimalPrecision)
	ten := big.NewInt(10)
	for unscaled.BitLen() > 0 && new(big.Int).Mod(unscaled, ten).Sign() == 0 {
		unscaled.Quo(unscaled, ten)
		exponent++
	}

	// Cast to int64; we know the magnitude is bounded by what fits in a
	// int64 because the parser rejects anything not representable as a
	// big.Rat from a decimal string of reasonable length, and the SDK
	// Unscaled field is int64 by spec.
	if !unscaled.IsInt64() {
		return common.Decimal{}, fmt.Errorf("%w: value too large for int64: %q", ErrInvalidDecimal, s)
	}
	return common.Decimal{Unscaled: unscaled.Int64(), Exponent: exponent}, nil
}

// DecimalString renders a *common.Decimal as a human-readable decimal
// string. Nil-safe: returns "" for nil.
//
// Examples (given Unscaled/Exponent):
//
//	{86, -2}    -> "0.86"
//	{1000, 0}   -> "1000"
//	{1, -4}     -> "0.0001"
func DecimalString(d *common.Decimal) string {
	if d == nil {
		return ""
	}
	if d.Exponent == 0 {
		return fmt.Sprintf("%d", d.Unscaled)
	}
	if d.Exponent > 0 {
		// Unscaled * 10^Exponent — e.g. {12, 2} = 1200
		scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(d.Exponent)), nil)
		prod := new(big.Int).Mul(big.NewInt(d.Unscaled), scale)
		return prod.String()
	}
	// Exponent < 0 — fractional. Pad with leading zeros as needed.
	unscaled := d.Unscaled
	exp := int(-d.Exponent)
	s := fmt.Sprintf("%d", unscaled)
	if unscaled < 0 {
		// Negative not expected for FX rates, but handle defensively.
		s = s[1:] // strip sign
	}
	digits := len(s)
	if digits > exp {
		// No leading zero needed: split "1234" with exp=2 -> "12.34"
		return s[:len(s)-exp] + "." + s[len(s)-exp:]
	}
	// Leading zeros: "5" with exp=4 -> "0.0005"; "" with exp=2 -> "0.00"
	return "0." + strings.Repeat("0", exp-digits) + s
}

// NewDecimalFromInt constructs a common.Decimal for an integer value
// (exponent=0). Use this for max_amount bands (which the protocol
// constrains to specific integer USD values like 1000, 5000, etc.).
func NewDecimalFromInt(v int64) common.Decimal {
	return common.Decimal{Unscaled: v, Exponent: 0}
}

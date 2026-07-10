package paymentintent

import (
	"fmt"
	"math/big"

	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
)

// DecimalString formats a Decimal as a human-readable decimal string.
func DecimalString(d *Decimal) string {
	if d == nil {
		return ""
	}
	return decimalFromSDK(&common.Decimal{Unscaled: d.Unscaled, Exponent: d.Exponent}).String()
}

// DecimalFromString parses a decimal string into a Decimal.
func DecimalFromString(s string) (*Decimal, error) {
	rat := new(big.Rat)
	if _, ok := rat.SetString(s); !ok {
		return nil, fmt.Errorf("cannot parse decimal: %s", s)
	}
	num := rat.Num()
	den := rat.Denom()

	// Convert to unscaled * 10^exponent form.
	exp := int32(0)
	for den.Cmp(big.NewInt(1)) > 0 && den.Mod(den, big.NewInt(10)).Sign() == 0 {
		exp--
		den.Div(den, big.NewInt(10))
	}
	if den.Cmp(big.NewInt(1)) != 0 {
		return nil, fmt.Errorf("decimal has non-power-of-10 denominator: %s", s)
	}

	unscaled := num.Int64()
	for exp < 0 {
		unscaled *= 10
		exp++
	}
	for exp > 0 {
		unscaled /= 10
		exp--
	}

	return &Decimal{Unscaled: unscaled, Exponent: exp}, nil
}

// FromCommon converts a SDK Decimal to a local Decimal.
func FromCommon(d *common.Decimal) *Decimal {
	if d == nil {
		return nil
	}
	return &Decimal{Unscaled: d.Unscaled, Exponent: d.Exponent}
}

// ToCommon converts a local Decimal to a SDK Decimal.
func ToCommon(d *Decimal) *common.Decimal {
	if d == nil {
		return nil
	}
	return &common.Decimal{Unscaled: d.Unscaled, Exponent: d.Exponent}
}

type decimal struct {
	unscaled int64
	exponent int32
}

func decimalFromSDK(d *common.Decimal) *decimal {
	if d == nil {
		return nil
	}
	return &decimal{unscaled: d.Unscaled, exponent: d.Exponent}
}

func (d *decimal) String() string {
	if d == nil {
		return ""
	}
	if d.exponent == 0 {
		return fmt.Sprintf("%d", d.unscaled)
	}
	value := new(big.Rat).SetInt64(d.unscaled)
	value.Quo(value, new(big.Rat).SetFloat64(float64Pow10(d.exponent)))
	return value.FloatString(int(-d.exponent))
}

func float64Pow10(exp int32) float64 {
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

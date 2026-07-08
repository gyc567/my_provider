package api

import (
	"strings"
	"testing"

	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
)

func TestParseDecimal_HappyPath(t *testing.T) {
	t.Parallel()
	// Note: ParseDecimal trims trailing zeros to produce a canonical
	// (Unscaled, Exponent) pair, and rejects "0" (rate must be > 0).
	cases := []struct {
		name     string
		in       string
		unscaled int64
		exp      int32
	}{
		{"integer", "1000", 1, 3},       // 1000 = 1*10^3 (canonical)
		{"two_decimals", "0.86", 86, -2},
		{"one_decimal", "0.8", 8, -1},
		{"trailing_zeros_trimmed", "0.8600", 86, -2},
		{"leading_dot", ".5", 5, -1},
		{"large", "1234567890.12345678", 123456789012345678, -8},
		{"rate_example_eur", "0.92", 92, -2},
		{"rate_small", "0.0001", 1, -4},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseDecimal(tc.in)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.Unscaled != tc.unscaled {
				t.Errorf("Unscaled: got %d, want %d", got.Unscaled, tc.unscaled)
			}
			if got.Exponent != tc.exp {
				t.Errorf("Exponent: got %d, want %d", got.Exponent, tc.exp)
			}
		})
	}
}

func TestParseDecimal_Rejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"non_numeric", "abc"},
		{"negative", "-0.86"},
		{"zero_is_rejected", "0"},
		{"two_dots", "0..86"},
		{"plus_sign", "+0.86"},
		{"whitespace_only", "   "},
		{"scientific", "1e-5"},
		{"too_many_decimals", "0.123456789"}, // > 8 decimals
		{"comma_decimal", "0,86"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseDecimal(tc.in)
			if err == nil {
				t.Fatalf("expected err for %q, got nil", tc.in)
			}
			// We want ErrInvalidDecimal style; assert message has "decimal"
			if !strings.Contains(err.Error(), "decimal") {
				t.Errorf("err message should mention 'decimal', got: %v", err)
			}
		})
	}
}

func TestDecimalString_RoundTrip(t *testing.T) {
	t.Parallel()
	// Round-trip: ParseDecimal canonicalizes, DecimalString re-renders to
	// the same canonical form. Inputs with trailing zeros are normalized
	// (e.g. "0.8600" -> "0.86").
	cases := []struct {
		in, want string
	}{
		{"0.86", "0.86"},
		{"1000", "1000"},
		{"0.001", "0.001"},
		{"1234567.89", "1234567.89"},
		{"0.8600", "0.86"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			d, err := ParseDecimal(tc.in)
			if err != nil {
				t.Fatalf("ParseDecimal: %v", err)
			}
			got := DecimalString(&d)
			if got != tc.want {
				t.Errorf("round-trip: in=%q out=%q want=%q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDecimalString_Nil(t *testing.T) {
	t.Parallel()
	if got := DecimalString(nil); got != "" {
		t.Errorf("DecimalString(nil): got %q, want empty", got)
	}
}

func TestNewDecimalFromInt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       int64
		unscaled int64
		exp      int32
	}{
		{0, 0, 0},
		{1000, 1000, 0},
		{250000, 250000, 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run("", func(t *testing.T) {
			t.Parallel()
			got := NewDecimalFromInt(tc.in)
			if got.Unscaled != tc.unscaled || got.Exponent != tc.exp {
				t.Errorf("NewDecimalFromInt(%d): got {%d, %d}, want {%d, %d}",
					tc.in, got.Unscaled, got.Exponent, tc.unscaled, tc.exp)
			}
		})
	}
}

// Sanity: ParseDecimal("0.86") produces a Decimal that, when checked
// against common.Decimal{Unscaled:86, Exponent:-2}, is equal.
func TestParseDecimal_MatchesLiteral(t *testing.T) {
	t.Parallel()
	got, err := ParseDecimal("0.86")
	if err != nil {
		t.Fatal(err)
	}
	want := common.Decimal{Unscaled: 86, Exponent: -2}
	if got.Unscaled != want.Unscaled || got.Exponent != want.Exponent {
		t.Errorf("got {%d, %d}, want {%d, %d}",
			got.Unscaled, got.Exponent, want.Unscaled, want.Exponent)
	}
}

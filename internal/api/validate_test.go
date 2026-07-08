package api

import (
	"strings"
	"testing"
)

func TestValidate_HappyPath(t *testing.T) {
	t.Parallel()
	req := UpdatePayOutRequest{Groups: []UpdatePayOutGroup{{
		Currency: "EUR", PaymentMethod: "SEPA", ExpirationSeconds: 30,
		Bands: []UpdatePayOutBand{
			{ClientQuoteID: "c1", MaxAmountUSD: "1000", Rate: "0.86"},
			{ClientQuoteID: "c2", MaxAmountUSD: "10000", Rate: "0.87"},
		},
	}}}
	if err := req.Validate(); err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}
}

func TestValidate_RejectsEmpty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		req  UpdatePayOutRequest
		code string
	}{
		{
			name: "no_groups",
			req:  UpdatePayOutRequest{Groups: nil},
			code: "invalid_request",
		},
		{
			name: "empty_groups",
			req:  UpdatePayOutRequest{Groups: []UpdatePayOutGroup{}},
			code: "invalid_request",
		},
		{
			name: "empty_bands",
			req: UpdatePayOutRequest{Groups: []UpdatePayOutGroup{{
				Currency: "EUR", PaymentMethod: "SEPA", ExpirationSeconds: 30,
				Bands: []UpdatePayOutBand{},
			}}},
			code: "invalid_request",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.req.Validate()
			if err == nil {
				t.Fatalf("expected err, got nil")
			}
			if err.Code != tc.code {
				t.Errorf("err code: got %q, want %q", err.Code, tc.code)
			}
		})
	}
}

func TestValidate_RejectsBadCurrency(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, cur string }{
		{"lowercase", "eur"},
		{"too_short", "EU"},
		{"too_long", "EURX"},
		{"not_iso", "XYZ"},
		{"empty", ""},
		{"numeric", "123"},
		{"with_space", "EU R"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := UpdatePayOutRequest{Groups: []UpdatePayOutGroup{{
				Currency: tc.cur, PaymentMethod: "SEPA", ExpirationSeconds: 30,
				Bands: []UpdatePayOutBand{
					{ClientQuoteID: "c1", MaxAmountUSD: "1000", Rate: "0.86"},
				},
			}}}
			err := req.Validate()
			if err == nil {
				t.Fatalf("expected err, got nil")
			}
			if err.Code != "invalid_currency" {
				t.Errorf("err code: got %q, want invalid_currency; detail: %s", err.Code, err.Detail)
			}
		})
	}
}

func TestValidate_RejectsBadPaymentMethod(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, pm string }{
		{"unspecified", "UNSPECIFIED"},
		{"empty", ""},
		{"unknown", "FOO"},
		{"lowercase", "sepa"},
		{"with_prefix", "PAYMENT_METHOD_TYPE_SEPA"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := UpdatePayOutRequest{Groups: []UpdatePayOutGroup{{
				Currency: "EUR", PaymentMethod: tc.pm, ExpirationSeconds: 30,
				Bands: []UpdatePayOutBand{
					{ClientQuoteID: "c1", MaxAmountUSD: "1000", Rate: "0.86"},
				},
			}}}
			err := req.Validate()
			if err == nil {
				t.Fatalf("expected err, got nil")
			}
			if err.Code != "invalid_payment_method" {
				t.Errorf("err code: got %q, want invalid_payment_method; detail: %s", err.Code, err.Detail)
			}
		})
	}
}

func TestValidate_RejectsBadExpiration(t *testing.T) {
	t.Parallel()
	cases := []int32{0, 1, 4, 301, 1000, -1}
	for _, sec := range cases {
		sec := sec
		t.Run("", func(t *testing.T) {
			t.Parallel()
			req := UpdatePayOutRequest{Groups: []UpdatePayOutGroup{{
				Currency: "EUR", PaymentMethod: "SEPA", ExpirationSeconds: sec,
				Bands: []UpdatePayOutBand{
					{ClientQuoteID: "c1", MaxAmountUSD: "1000", Rate: "0.86"},
				},
			}}}
			err := req.Validate()
			if err == nil {
				t.Fatalf("expected err, got nil")
			}
			if err.Code != "invalid_expiration" {
				t.Errorf("err code: got %q, want invalid_expiration", err.Code)
			}
		})
	}
}

func TestValidate_RejectsBadMaxAmount(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, amt string }{
		{"too_small", "999"},
		{"not_in_whitelist", "2000"},
		{"not_in_whitelist_2", "100"},
		{"zero", "0"},
		{"empty", ""},
		{"non_numeric", "abc"},
		{"decimal", "1000.5"},
		{"negative", "-1000"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := UpdatePayOutRequest{Groups: []UpdatePayOutGroup{{
				Currency: "EUR", PaymentMethod: "SEPA", ExpirationSeconds: 30,
				Bands: []UpdatePayOutBand{
					{ClientQuoteID: "c1", MaxAmountUSD: tc.amt, Rate: "0.86"},
				},
			}}}
			err := req.Validate()
			if err == nil {
				t.Fatalf("expected err, got nil")
			}
			if err.Code != "unsupported_band" {
				t.Errorf("err code: got %q, want unsupported_band; detail: %s", err.Code, err.Detail)
			}
		})
	}
}

func TestValidate_AllowsValidMaxAmount(t *testing.T) {
	t.Parallel()
	for _, amt := range []string{"1000", "5000", "10000", "25000", "250000", "1000000"} {
		amt := amt
		t.Run(amt, func(t *testing.T) {
			t.Parallel()
			req := UpdatePayOutRequest{Groups: []UpdatePayOutGroup{{
				Currency: "EUR", PaymentMethod: "SEPA", ExpirationSeconds: 30,
				Bands: []UpdatePayOutBand{
					{ClientQuoteID: "c1", MaxAmountUSD: amt, Rate: "0.86"},
				},
			}}}
			if err := req.Validate(); err != nil {
				t.Errorf("max_amount=%s should be allowed, got: %v", amt, err)
			}
		})
	}
}

func TestValidate_RejectsBadRate(t *testing.T) {
	t.Parallel()
	cases := []string{"0", "0.0", "-0.86", "abc", ""}
	for _, r := range cases {
		r := r
		t.Run(r, func(t *testing.T) {
			t.Parallel()
			req := UpdatePayOutRequest{Groups: []UpdatePayOutGroup{{
				Currency: "EUR", PaymentMethod: "SEPA", ExpirationSeconds: 30,
				Bands: []UpdatePayOutBand{
					{ClientQuoteID: "c1", MaxAmountUSD: "1000", Rate: r},
				},
			}}}
			err := req.Validate()
			if err == nil {
				t.Fatalf("expected err, got nil")
			}
			if err.Code != "invalid_rate" {
				t.Errorf("err code: got %q, want invalid_rate", err.Code)
			}
		})
	}
}

func TestValidate_RejectsBadClientQuoteID(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, id string }{
		{"empty", ""},
		{"too_long_65", strings.Repeat("a", 65)},
		{"too_long_1000", strings.Repeat("a", 1000)},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := UpdatePayOutRequest{Groups: []UpdatePayOutGroup{{
				Currency: "EUR", PaymentMethod: "SEPA", ExpirationSeconds: 30,
				Bands: []UpdatePayOutBand{
					{ClientQuoteID: tc.id, MaxAmountUSD: "1000", Rate: "0.86"},
				},
			}}}
			err := req.Validate()
			if err == nil {
				t.Fatalf("expected err, got nil")
			}
			if err.Code != "invalid_client_quote_id" {
				t.Errorf("err code: got %q, want invalid_client_quote_id; detail: %s", err.Code, err.Detail)
			}
		})
	}
}

func TestValidate_AllowsValidClientQuoteID(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"a", strings.Repeat("a", 64), "c-2026-07-08-abc1"} {
		id := id
		t.Run("", func(t *testing.T) {
			t.Parallel()
			req := UpdatePayOutRequest{Groups: []UpdatePayOutGroup{{
				Currency: "EUR", PaymentMethod: "SEPA", ExpirationSeconds: 30,
				Bands: []UpdatePayOutBand{
					{ClientQuoteID: id, MaxAmountUSD: "1000", Rate: "0.86"},
				},
			}}}
			if err := req.Validate(); err != nil {
				t.Errorf("client_quote_id=%q should be allowed, got: %v", id, err)
			}
		})
	}
}

func TestValidate_RejectsDuplicateClientQuoteIDInGroup(t *testing.T) {
	t.Parallel()
	req := UpdatePayOutRequest{Groups: []UpdatePayOutGroup{{
		Currency: "EUR", PaymentMethod: "SEPA", ExpirationSeconds: 30,
		Bands: []UpdatePayOutBand{
			{ClientQuoteID: "c1", MaxAmountUSD: "1000", Rate: "0.86"},
			{ClientQuoteID: "c1", MaxAmountUSD: "5000", Rate: "0.87"},
		},
	}}}
	err := req.Validate()
	if err == nil {
		t.Fatalf("expected err, got nil")
	}
	if err.Code != "duplicate_client_quote_id" {
		t.Errorf("err code: got %q, want duplicate_client_quote_id", err.Code)
	}
}

func TestValidate_RejectsTooManyGroups(t *testing.T) {
	t.Parallel()
	groups := make([]UpdatePayOutGroup, 51)
	for i := range groups {
		groups[i] = UpdatePayOutGroup{
			Currency: "EUR", PaymentMethod: "SEPA", ExpirationSeconds: 30,
			Bands: []UpdatePayOutBand{{ClientQuoteID: "c1", MaxAmountUSD: "1000", Rate: "0.86"}},
		}
	}
	req := UpdatePayOutRequest{Groups: groups}
	err := req.Validate()
	if err == nil {
		t.Fatalf("expected err, got nil")
	}
	if err.Code != "invalid_request" {
		t.Errorf("err code: got %q, want invalid_request", err.Code)
	}
}

func TestValidate_RejectsTooManyBands(t *testing.T) {
	t.Parallel()
	bands := make([]UpdatePayOutBand, 21)
	for i := range bands {
		bands[i] = UpdatePayOutBand{
			ClientQuoteID: "c" + strings.Repeat("x", 10) + string(rune('a'+i%26)),
			MaxAmountUSD:  "1000",
			Rate:          "0.86",
		}
	}
	req := UpdatePayOutRequest{Groups: []UpdatePayOutGroup{{
		Currency: "EUR", PaymentMethod: "SEPA", ExpirationSeconds: 30, Bands: bands,
	}}}
	err := req.Validate()
	if err == nil {
		t.Fatalf("expected err, got nil")
	}
	if err.Code != "invalid_request" {
		t.Errorf("err code: got %q, want invalid_request", err.Code)
	}
}

func TestValidate_MultipleGroups(t *testing.T) {
	t.Parallel()
	req := UpdatePayOutRequest{Groups: []UpdatePayOutGroup{
		{Currency: "EUR", PaymentMethod: "SEPA", ExpirationSeconds: 30,
			Bands: []UpdatePayOutBand{{ClientQuoteID: "c1", MaxAmountUSD: "1000", Rate: "0.86"}}},
		{Currency: "GBP", PaymentMethod: "SWIFT", ExpirationSeconds: 60,
			Bands: []UpdatePayOutBand{{ClientQuoteID: "c2", MaxAmountUSD: "5000", Rate: "0.79"}}},
	}}
	if err := req.Validate(); err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}
}

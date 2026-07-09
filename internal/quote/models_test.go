package quote

import (
	"testing"
	"time"
)

func TestDecimalFloat64(t *testing.T) {
	tests := []struct {
		name string
		d    Decimal
		want float64
	}{
		{"integer", Decimal{Unscaled: 1000, Exponent: 0}, 1000.0},
		{"two decimals", Decimal{Unscaled: 86, Exponent: -2}, 0.86},
		{"negative exponent", Decimal{Unscaled: 500, Exponent: 0}, 500.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.d.Float64(); got != tt.want {
				t.Errorf("Float64() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateQuoteGroup(t *testing.T) {
	makeValidGroup := func() QuoteGroup {
		return QuoteGroup{
			Currency:      "EUR",
			PaymentMethod: "PAYMENT_METHOD_TYPE_SEPA",
			Expiration:    time.Now().Add(time.Hour),
			Timestamp:     time.Now(),
			Bands: []Band{
				{ClientQuoteID: "eur-sepa-1k", MaxAmount: Decimal{Unscaled: 1000, Exponent: 0}, Rate: Decimal{Unscaled: 86, Exponent: -2}},
			},
		}
	}

	tests := []struct {
		name     string
		mutate   func(g QuoteGroup) QuoteGroup
		wantErr  bool
	}{
		{"valid", func(g QuoteGroup) QuoteGroup { return g }, false},
		{"missing currency", func(g QuoteGroup) QuoteGroup { g.Currency = ""; return g }, true},
		{"lowercase currency", func(g QuoteGroup) QuoteGroup { g.Currency = "eur"; return g }, true},
		{"missing payment method", func(g QuoteGroup) QuoteGroup { g.PaymentMethod = ""; return g }, true},
		{"missing expiration", func(g QuoteGroup) QuoteGroup { g.Expiration = time.Time{}; return g }, true},
		{"missing timestamp", func(g QuoteGroup) QuoteGroup { g.Timestamp = time.Time{}; return g }, true},
		{"no bands", func(g QuoteGroup) QuoteGroup { g.Bands = nil; return g }, true},
		{"missing client quote id", func(g QuoteGroup) QuoteGroup { g.Bands[0].ClientQuoteID = ""; return g }, true},
		{"non-standard band", func(g QuoteGroup) QuoteGroup { g.Bands[0].MaxAmount = Decimal{Unscaled: 1234, Exponent: 0}; return g }, true},
		{"duplicate max amount", func(g QuoteGroup) QuoteGroup {
			g.Bands = append(g.Bands, Band{ClientQuoteID: "eur-sepa-1k-2", MaxAmount: Decimal{Unscaled: 1000, Exponent: 0}, Rate: Decimal{Unscaled: 85, Exponent: -2}})
			return g
		}, true},
		{"duplicate client quote id", func(g QuoteGroup) QuoteGroup {
			g.Bands = append(g.Bands, Band{ClientQuoteID: "eur-sepa-1k", MaxAmount: Decimal{Unscaled: 5000, Exponent: 0}, Rate: Decimal{Unscaled: 85, Exponent: -2}})
			return g
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group := tt.mutate(makeValidGroup())
			err := ValidateQuoteGroup(group)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateQuoteGroup() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSnapshot(t *testing.T) {
	base := QuoteGroup{
		Currency:      "EUR",
		PaymentMethod: "PAYMENT_METHOD_TYPE_SEPA",
		Expiration:    time.Now().Add(time.Hour),
		Timestamp:     time.Now(),
		Bands: []Band{
			{ClientQuoteID: "eur-sepa-1k", MaxAmount: Decimal{Unscaled: 1000, Exponent: 0}, Rate: Decimal{Unscaled: 86, Exponent: -2}},
		},
	}

	tests := []struct {
		name    string
		groups  []QuoteGroup
		wantErr bool
	}{
		{"valid single", []QuoteGroup{base}, false},
		{"valid multiple currencies", []QuoteGroup{
			base,
			{Currency: "GBP", PaymentMethod: "PAYMENT_METHOD_TYPE_SWIFT", Expiration: base.Expiration, Timestamp: base.Timestamp, Bands: []Band{
				{ClientQuoteID: "gbp-swift-1k", MaxAmount: Decimal{Unscaled: 1000, Exponent: 0}, Rate: Decimal{Unscaled: 79, Exponent: -2}},
			}},
		}, false},
		{"duplicate currency/method", []QuoteGroup{base, base}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSnapshot(tt.groups)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSnapshot() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestQuoteGroup_Validate(t *testing.T) {
	g := QuoteGroup{
		Currency:      "EUR",
		PaymentMethod: "PAYMENT_METHOD_TYPE_SEPA",
		Expiration:    time.Now().Add(time.Hour),
		Timestamp:     time.Now(),
		Bands: []Band{
			{ClientQuoteID: "eur-sepa-1k", MaxAmount: Decimal{Unscaled: 1000, Exponent: 0}, Rate: Decimal{Unscaled: 86, Exponent: -2}},
		},
	}
	if err := g.Validate(); err != nil {
		t.Errorf("Validate() error = %v", err)
	}

	invalid := g
	invalid.Currency = ""
	if err := invalid.Validate(); err == nil {
		t.Error("expected error for invalid group")
	}
}

func TestStreamType_ValidAndValidate(t *testing.T) {
	if !StreamTypePayOut.Valid() {
		t.Error("expected StreamTypePayOut to be valid")
	}
	if !StreamTypePayIn.Valid() {
		t.Error("expected StreamTypePayIn to be valid")
	}
	if StreamType("unknown").Valid() {
		t.Error("expected unknown stream type to be invalid")
	}
	if err := StreamTypePayOut.Validate(); err != nil {
		t.Errorf("Validate() error = %v", err)
	}
	if err := StreamType("unknown").Validate(); err == nil {
		t.Error("expected Validate to return error for unknown stream type")
	}
}

func TestStandardBandAmounts(t *testing.T) {
	if len(StandardBandAmounts) != 6 {
		t.Fatalf("expected 6 standard bands, got %d", len(StandardBandAmounts))
	}
	expected := map[int64]bool{1000: true, 5000: true, 10000: true, 25000: true, 250000: true, 1000000: true}
	for _, amount := range StandardBandAmounts {
		if !expected[amount] {
			t.Errorf("unexpected standard band amount: %d", amount)
		}
	}
}

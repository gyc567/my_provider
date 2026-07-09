package api

import "my-provider/internal/quote"

// UpdateQuotesRequest is the payload for PUT /api/v1/quotes/{stream}.
type UpdateQuotesRequest struct {
	Groups []quote.QuoteGroup `json:"groups" validate:"required"`
}

// QuotesResponse is returned by GET /api/v1/quotes.
type QuotesResponse struct {
	PayOut []quote.QuoteGroup `json:"payOut"`
	PayIn  []quote.QuoteGroup `json:"payIn"`
}

// GetNetworkQuoteRequest is the payload for POST /api/v1/quotes/network.
type GetNetworkQuoteRequest struct {
	Amount         quote.Decimal `json:"amount" validate:"required"`
	AmountType     string        `json:"amountType" validate:"required,oneof=pay_out settlement"`
	PayOutCurrency string        `json:"payOutCurrency" validate:"required,len=3,uppercase"`
	PayOutMethod   string        `json:"payOutMethod" validate:"required"`
}

// PublishResponse is returned by POST /api/v1/quotes/publish.
type PublishResponse struct {
	Published bool   `json:"published"`
	Message   string `json:"message"`
}

// ErrorResponse is the standard error body.
type ErrorResponse struct {
	Error string `json:"error"`
}

// APIDecimal is the JSON shape of every Decimal field exposed by this API.
// It is used in Swagger-only schema types so the generated docs match the wire format.
type APIDecimal struct {
	Unscaled int64 `json:"unscaled"`
	Exponent int32 `json:"exponent"`
}

// SwaggerQuotesResponse mirrors QuotesResponse with accurate Decimal schema for Swagger.
type SwaggerQuotesResponse struct {
	PayOut []SwaggerQuoteGroup `json:"payOut"`
	PayIn  []SwaggerQuoteGroup `json:"payIn"`
}

// SwaggerQuoteGroup mirrors quote.QuoteGroup with accurate Decimal schema for Swagger.
type SwaggerQuoteGroup struct {
	Currency      string            `json:"currency"`
	PaymentMethod string            `json:"paymentMethod"`
	Expiration    string            `json:"expiration"`
	Timestamp     string            `json:"timestamp"`
	Bands         []SwaggerBand     `json:"bands"`
}

// SwaggerBand mirrors quote.Band with accurate Decimal schema for Swagger.
type SwaggerBand struct {
	ClientQuoteID string     `json:"clientQuoteId"`
	MaxAmount     APIDecimal `json:"maxAmount"`
	Rate          APIDecimal `json:"rate"`
	Fix           *APIDecimal `json:"fix,omitempty"`
}

// SwaggerUpdateQuotesRequest mirrors UpdateQuotesRequest with accurate Decimal schema for Swagger.
type SwaggerUpdateQuotesRequest struct {
	Groups []SwaggerQuoteGroup `json:"groups"`
}

// SwaggerGetNetworkQuoteRequest mirrors GetNetworkQuoteRequest with accurate Decimal schema for Swagger.
type SwaggerGetNetworkQuoteRequest struct {
	Amount         APIDecimal `json:"amount"`
	AmountType     string     `json:"amountType"`
	PayOutCurrency string     `json:"payOutCurrency"`
	PayOutMethod   string     `json:"payOutMethod"`
}

// NetworkQuoteResponse mirrors the JSON shape returned by POST /api/v1/quotes/network.
type NetworkQuoteResponse struct {
	Result    *NetworkQuoteResult    `json:"result"`
	AllQuotes []NetworkProviderQuote `json:"allQuotes"`
}

// NetworkQuoteResult wraps the oneof success/failure payload from the network.
type NetworkQuoteResult struct {
	Success *NetworkQuoteSuccess `json:"success"`
	Failure *NetworkQuoteFailure `json:"failure"`
}

// NetworkQuoteSuccess represents a successful network quote.
type NetworkQuoteSuccess struct {
	Rate             APIDecimal     `json:"rate"`
	Expiration       string         `json:"expiration"`
	QuoteID          NetworkQuoteID `json:"quoteId"`
	PayOutAmount     APIDecimal     `json:"payOutAmount"`
	SettlementAmount APIDecimal     `json:"settlementAmount"`
}

// NetworkQuoteFailure represents a network quote failure.
type NetworkQuoteFailure struct {
	Reason string `json:"reason"`
}

// NetworkQuoteID identifies a quote within a provider.
type NetworkQuoteID struct {
	QuoteID    int64 `json:"quoteId"`
	ProviderID int32 `json:"providerId"`
}

// NetworkProviderQuote is an alternative quote from a single provider.
type NetworkProviderQuote struct {
	QuoteID      NetworkQuoteID    `json:"quoteId"`
	Rate         APIDecimal        `json:"rate"`
	Expiration   string            `json:"expiration"`
	PayOutAmount APIDecimal        `json:"payOutAmount"`
	Settlement   NetworkSettlement `json:"settlement"`
	Executable   bool              `json:"executable"`
}

// NetworkSettlement contains settlement details for a provider quote.
type NetworkSettlement struct {
	Amount           APIDecimal `json:"amount"`
	CreditLimit      APIDecimal `json:"creditLimit"`
	TotalUsed        APIDecimal `json:"totalUsed"`
	PrefundingAmount APIDecimal `json:"prefundingAmount"`
}

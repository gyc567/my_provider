package api

// UpdatePayOutRequest is the JSON request body for POST /api/v1/quotes/pay-out.
//
// One HTTP call MUST carry a full snapshot of all (currency, payment_method)
// groups we want sandbox to see — sandbox's UpdateQuote RPC atomically
// replaces the prior snapshot, so anything not in this payload will be
// removed from routing.
type UpdatePayOutRequest struct {
	Groups []UpdatePayOutGroup `json:"groups"`
}

type UpdatePayOutGroup struct {
	Currency          string                `json:"currency"`
	PaymentMethod     string                `json:"payment_method"`
	ExpirationSeconds int32                 `json:"expiration_seconds"`
	Bands             []UpdatePayOutBand    `json:"bands"`
}

type UpdatePayOutBand struct {
	ClientQuoteID string `json:"client_quote_id"`
	MaxAmountUSD  string `json:"max_amount_usd"`
	Rate          string `json:"rate"`
}

// UpdatePayOutResponse is the JSON response body for successful 200.
type UpdatePayOutResponse struct {
	Status          string `json:"status"`            // always "OK"
	AppliedAt       string `json:"applied_at"`        // RFC3339
	ExpiresAt       string `json:"expires_at"`        // RFC3339
	GroupsPublished int    `json:"groups_published"`
	BandsPublished  int    `json:"bands_published"`
	RequestID       string `json:"request_id,omitempty"`
}

// ErrorResponse is the JSON envelope for all non-2xx responses.
//
// Note: the *error mapping* type (also called APIError) lives in
// error_mapping.go. The two serve different purposes — this is the wire
// format, error_mapping's APIError is the in-process transport from the
// handler to the writer.
//
// We avoid the name collision by giving the wire type a distinct name
// even though they share JSON keys.
type ErrorResponse struct {
	Error     string `json:"error"`
	Detail    string `json:"detail,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

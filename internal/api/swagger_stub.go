package api

// UpdatePayOut pushes a pay-out quote snapshot to the t-0 network.
// The actual handler is implemented by Handler.ServeHTTP; this stub exists
// solely so swag can generate the OpenAPI contract for the route.
//
// @Summary Push pay-out quotes to the network
// @Tags quotes
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param Idempotency-Key header string false "Optional idempotency key; if omitted, one is derived from the body hash"
// @Param body body UpdatePayOutRequest true "Pay-out quote groups"
// @Success 200 {object} UpdatePayOutResponse
// @Failure 400 {object} ErrorResponse
// @Failure 401 {object} ErrorResponse
// @Failure 409 {object} ErrorResponse
// @Failure 422 {object} ErrorResponse
// @Failure 502 {object} ErrorResponse
// @Failure 504 {object} ErrorResponse
// @Router /api/v1/quotes/pay-out [post]
func UpdatePayOut() {}

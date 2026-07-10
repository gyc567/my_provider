package recipient

import (
	"context"

	"connectrpc.com/connect"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	sdkrecipient "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/recipient"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/recipient/recipientconnect"
	"my-provider/internal/paymentintent"
)

// NetworkClient wraps the SDK NetworkServiceClient for payment intent recipients.
type NetworkClient interface {
	CreatePaymentIntent(ctx context.Context, req *connect.Request[sdkrecipient.CreatePaymentIntentRequest]) (*connect.Response[sdkrecipient.CreatePaymentIntentResponse], error)
	GetQuote(ctx context.Context, req *connect.Request[sdkrecipient.GetQuoteRequest]) (*connect.Response[sdkrecipient.GetQuoteResponse], error)
}

// NewNetworkClient adapts the generated SDK client to the local interface.
func NewNetworkClient(client recipientconnect.NetworkServiceClient) NetworkClient {
	return client
}

// CreatePaymentIntent submits a new payment intent to the t-0 Network on behalf of a beneficiary.
func CreatePaymentIntent(ctx context.Context, client NetworkClient, req CreatePaymentIntentRequest) (*sdkrecipient.CreatePaymentIntentResponse, error) {
	sdkReq := &sdkrecipient.CreatePaymentIntentRequest{
		PaymentReference: req.PaymentReference,
		PayInCurrency:    req.PayInCurrency,
		PayInAmount:      paymentintent.ToCommon(req.PayInAmount),
		PayOutCurrency:   req.PayOutCurrency,
		PayOutDetails:    req.PayOutDetails,
	}
	resp, err := client.CreatePaymentIntent(ctx, connect.NewRequest(sdkReq))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// GetQuote requests an indicative quote for a beneficiary payment intent.
func GetQuote(ctx context.Context, client NetworkClient, req GetQuoteRequest) (*sdkrecipient.GetQuoteResponse, error) {
	sdkReq := &sdkrecipient.GetQuoteRequest{
		PayInCurrency:       req.PayInCurrency,
		PayInAmount:         paymentintent.ToCommon(req.PayInAmount),
		PayOutCurrency:      req.PayOutCurrency,
		PayInPaymentMethod:  common.PaymentMethodType(common.PaymentMethodType_value[req.PayInPaymentMethod]),
		PayOutPaymentMethod: common.PaymentMethodType(common.PaymentMethodType_value[req.PayOutPaymentMethod]),
	}
	resp, err := client.GetQuote(ctx, connect.NewRequest(sdkReq))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// CreatePaymentIntentRequest is the domain request for creating a beneficiary payment intent.
type CreatePaymentIntentRequest struct {
	PaymentReference string
	PayInCurrency    string
	PayInAmount      *paymentintent.Decimal
	PayOutCurrency   string
	PayOutDetails    *common.PaymentDetails
}

// GetQuoteRequest is the domain request for a beneficiary indicative quote.
type GetQuoteRequest struct {
	PayInCurrency       string
	PayInAmount         *paymentintent.Decimal
	PayOutCurrency      string
	PayInPaymentMethod  string
	PayOutPaymentMethod string
}

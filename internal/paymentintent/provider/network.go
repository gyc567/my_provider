package provider

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	sdkprovider "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/provider"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/provider/providerconnect"
	"my-provider/internal/paymentintent"
)

// NetworkClient wraps the SDK NetworkServiceClient for payment intents.
type NetworkClient interface {
	ConfirmPayment(ctx context.Context, req *connect.Request[sdkprovider.ConfirmPaymentRequest]) (*connect.Response[sdkprovider.ConfirmPaymentResponse], error)
	RejectPaymentIntent(ctx context.Context, req *connect.Request[sdkprovider.RejectPaymentIntentRequest]) (*connect.Response[sdkprovider.RejectPaymentIntentResponse], error)
	ConfirmSettlement(ctx context.Context, req *connect.Request[sdkprovider.ConfirmSettlementRequest]) (*connect.Response[sdkprovider.ConfirmSettlementResponse], error)
}

// NewNetworkClient adapts the generated SDK client to the local interface.
func NewNetworkClient(client providerconnect.NetworkServiceClient) NetworkClient {
	return client
}

// ConfirmPayment notifies the network that fiat funds have been received.
func ConfirmPayment(ctx context.Context, client NetworkClient, id uint64, method common.PaymentMethodType) (*sdkprovider.ConfirmPaymentResponse, error) {
	resp, err := client.ConfirmPayment(ctx, connect.NewRequest(&sdkprovider.ConfirmPaymentRequest{
		PaymentIntentId: id,
		PaymentMethod:   method,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// RejectPaymentIntent notifies the network that the payment intent is rejected.
func RejectPaymentIntent(ctx context.Context, client NetworkClient, id uint64, reason string) error {
	_, err := client.RejectPaymentIntent(ctx, connect.NewRequest(&sdkprovider.RejectPaymentIntentRequest{
		PaymentIntentId: id,
		Reason:          reason,
	}))
	return err
}

// ConfirmSettlement links a settlement transaction to payment intents.
func ConfirmSettlement(ctx context.Context, client NetworkClient, blockchain common.Blockchain, txHash string, ids []uint64) error {
	_, err := client.ConfirmSettlement(ctx, connect.NewRequest(&sdkprovider.ConfirmSettlementRequest{
		Blockchain:      blockchain,
		TxHash:          txHash,
		PaymentIntentId: ids,
	}))
	return err
}

// PaymentIntentResponse is the REST response shape for a payment intent.
type PaymentIntentResponse struct {
	ID                uint64                  `json:"id"`
	Role              string                  `json:"role"`
	Currency          string                  `json:"currency"`
	Amount            string                  `json:"amount"`
	MerchantID        uint32                  `json:"merchantId"`
	PaymentMethod     string                  `json:"paymentMethod"`
	PaymentURL        string                  `json:"paymentUrl"`
	Status            string                  `json:"status"`
	SettlementAmount  string                  `json:"settlementAmount,omitempty"`
	PayoutProviderID  uint32                  `json:"payoutProviderId,omitempty"`
	FundsReceivedAt   *string                 `json:"fundsReceivedAt,omitempty"`
	PayoutConfirmedAt *string                 `json:"payoutConfirmedAt,omitempty"`
	CreatedAt         string                  `json:"createdAt"`
}

// ToResponse converts a domain PaymentIntent to the REST response shape.
func ToResponse(pi *paymentintent.PaymentIntent, resp *sdkprovider.ConfirmPaymentResponse) PaymentIntentResponse {
	out := PaymentIntentResponse{
		ID:            pi.ID,
		Role:          string(pi.Role),
		Currency:      pi.Currency,
		Amount:        paymentintent.DecimalString(pi.Amount),
		MerchantID:    pi.MerchantID,
		PaymentMethod: pi.PaymentMethod,
		PaymentURL:    pi.PaymentURL,
		Status:        string(pi.Status),
		CreatedAt:     pi.CreatedAt.Format(time.RFC3339),
	}
	if pi.FundsReceivedAt != nil {
		t := pi.FundsReceivedAt.Format(time.RFC3339)
		out.FundsReceivedAt = &t
	}
	if pi.PayoutConfirmedAt != nil {
		t := pi.PayoutConfirmedAt.Format(time.RFC3339)
		out.PayoutConfirmedAt = &t
	}
	if resp != nil {
		out.SettlementAmount = paymentintent.DecimalString(paymentintent.FromCommon(resp.SettlementAmount))
		out.PayoutProviderID = resp.PayoutProviderId
	}
	return out
}

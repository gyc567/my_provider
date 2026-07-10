// Package provider implements the Phase 3A Pay-In Provider service handlers.
package provider

import (
	"context"
	"fmt"
	"log"
	"time"

	"connectrpc.com/connect"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	sdkprovider "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/provider"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/provider/providerconnect"
	"my-provider/internal/paymentintent"
)

// Handler implements the Pay-In Provider RPCs.
type Handler struct {
	store         paymentintent.Store
	networkClient NetworkClient
	paymentBaseURL string
}

// NewHandler creates a new Pay-In Provider handler.
func NewHandler(store paymentintent.Store, networkClient NetworkClient, paymentBaseURL string) *Handler {
	return &Handler{
		store:          store,
		networkClient:  networkClient,
		paymentBaseURL: paymentBaseURL,
	}
}

var _ providerconnect.ProviderServiceHandler = (*Handler)(nil)

// CreatePaymentIntent is called by the network when an end-user wants to pay in.
func (h *Handler) CreatePaymentIntent(
	ctx context.Context, req *connect.Request[sdkprovider.CreatePaymentIntentRequest],
) (*connect.Response[sdkprovider.CreatePaymentIntentResponse], error) {
	msg := req.Msg
	if msg.PaymentIntentId == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("payment_intent_id is required"))
	}

	pi := paymentintent.PaymentIntent{
		ID:           msg.PaymentIntentId,
		Role:         paymentintent.RolePayInProvider,
		Currency:     msg.Currency,
		Amount:       paymentintent.FromCommon(msg.Amount),
		MerchantID:   msg.MerchantId,
		PaymentURL:   fmt.Sprintf("%s/pay/%d", h.paymentBaseURL, msg.PaymentIntentId),
		Status:       paymentintent.StatusCreated,
	}
	if err := pi.Validate(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	record, created, err := h.store.GetOrCreate(ctx, pi)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	method := common.PaymentMethodType_PAYMENT_METHOD_TYPE_SEPA
	if record.PaymentMethod != "" {
		method = common.PaymentMethodType(common.PaymentMethodType_value[record.PaymentMethod])
	}

	log.Printf("CreatePaymentIntent received: id=%d currency=%s amount=%s merchant=%d created=%v\n",
		msg.PaymentIntentId, msg.Currency, paymentintent.DecimalString(paymentintent.FromCommon(msg.Amount)), msg.MerchantId, created)

	return connect.NewResponse(&sdkprovider.CreatePaymentIntentResponse{
		PaymentMethods: []*sdkprovider.CreatePaymentIntentResponse_PaymentMethod{
			{
				PaymentUrl:    record.PaymentURL,
				PaymentMethod: method,
			},
		},
	}), nil
}

// ConfirmPayout is called by the network when the crypto payout has been completed.
func (h *Handler) ConfirmPayout(
	ctx context.Context, req *connect.Request[sdkprovider.ConfirmPayoutRequest],
) (*connect.Response[sdkprovider.ConfirmPayoutResponse], error) {
	msg := req.Msg
	err := h.store.MarkPayoutConfirmed(ctx, msg.PaymentIntentId, paymentintent.RolePayInProvider, msg.PaymentId, time.Now())
	if err != nil {
		if err == paymentintent.ErrInvalidTransition {
			log.Printf("ConfirmPayout idempotent retry: id=%d payment_id=%d\n", msg.PaymentIntentId, msg.PaymentId)
			return connect.NewResponse(&sdkprovider.ConfirmPayoutResponse{}), nil
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	log.Printf("ConfirmPayout received: intent_id=%d payment_id=%d status=%s\n",
		msg.PaymentIntentId, msg.PaymentId, paymentintent.StatusPayoutConfirmed)

	return connect.NewResponse(&sdkprovider.ConfirmPayoutResponse{}), nil
}

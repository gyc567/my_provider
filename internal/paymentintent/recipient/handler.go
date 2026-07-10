// Package recipient implements the Phase 3B beneficiary (recipient) service handlers.
package recipient

import (
	"context"
	"log"
	"time"

	"connectrpc.com/connect"
	sdkrecipient "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/recipient"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/recipient/recipientconnect"
	"my-provider/internal/paymentintent"
)

// Handler implements the RecipientService RPCs for the beneficiary role.
type Handler struct {
	store paymentintent.Store
}

// NewHandler creates a new beneficiary recipient handler.
func NewHandler(store paymentintent.Store) *Handler {
	return &Handler{store: store}
}

var _ recipientconnect.RecipientServiceHandler = (*Handler)(nil)

// ConfirmPayIn is called by the network when the pay-in provider has received funds from the payer.
func (h *Handler) ConfirmPayIn(
	ctx context.Context, req *connect.Request[sdkrecipient.ConfirmPayInRequest],
) (*connect.Response[sdkrecipient.ConfirmPayInResponse], error) {
	msg := req.Msg
	err := h.store.MarkFundsReceived(ctx, msg.PaymentIntentId, paymentintent.RoleBeneficiary, time.Now(), 0)
	if err != nil {
		if err == paymentintent.ErrInvalidTransition {
			log.Printf("ConfirmPayIn idempotent retry: id=%d\n", msg.PaymentIntentId)
			return connect.NewResponse(&sdkrecipient.ConfirmPayInResponse{}), nil
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	log.Printf("ConfirmPayIn received: intent_id=%d status=%s\n",
		msg.PaymentIntentId, paymentintent.StatusFundsReceived)
	return connect.NewResponse(&sdkrecipient.ConfirmPayInResponse{}), nil
}

// ConfirmPayment is called by the network when the payout has been completed successfully.
func (h *Handler) ConfirmPayment(
	ctx context.Context, req *connect.Request[sdkrecipient.ConfirmPaymentRequest],
) (*connect.Response[sdkrecipient.ConfirmPaymentResponse], error) {
	msg := req.Msg
	err := h.store.MarkConfirmed(ctx, msg.PaymentIntentId, paymentintent.RoleBeneficiary, time.Now())
	if err != nil {
		if err == paymentintent.ErrInvalidTransition {
			log.Printf("ConfirmPayment idempotent retry: id=%d\n", msg.PaymentIntentId)
			return connect.NewResponse(&sdkrecipient.ConfirmPaymentResponse{}), nil
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	log.Printf("ConfirmPayment received: intent_id=%d status=%s\n",
		msg.PaymentIntentId, paymentintent.StatusConfirmed)
	return connect.NewResponse(&sdkrecipient.ConfirmPaymentResponse{}), nil
}

// RejectPaymentIntent is called by the network when the payment intent is rejected.
func (h *Handler) RejectPaymentIntent(
	ctx context.Context, req *connect.Request[sdkrecipient.RejectPaymentIntentRequest],
) (*connect.Response[sdkrecipient.RejectPaymentIntentResponse], error) {
	msg := req.Msg
	err := h.store.MarkRejected(ctx, msg.PaymentIntentId, paymentintent.RoleBeneficiary, msg.Reason, time.Now())
	if err != nil {
		if err == paymentintent.ErrInvalidTransition {
			log.Printf("RejectPaymentIntent idempotent retry: id=%d\n", msg.PaymentIntentId)
			return connect.NewResponse(&sdkrecipient.RejectPaymentIntentResponse{}), nil
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	log.Printf("RejectPaymentIntent received: intent_id=%d reason=%s status=%s\n",
		msg.PaymentIntentId, msg.Reason, paymentintent.StatusRejected)
	return connect.NewResponse(&sdkrecipient.RejectPaymentIntentResponse{}), nil
}


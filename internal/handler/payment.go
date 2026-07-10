// Package handler implements the t-0 Network ProviderService RPC handlers.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	"connectrpc.com/connect"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
	localpayment "my-provider/internal/payment"
	"my-provider/internal/settlement"
)

// amlAutoApproveDelay is how long the provider waits after returning
// ManualAmlCheck before calling CompleteManualAmlCheck(Approved).
// This is the Phase 2 smoke-test value; real AML processing should
// drive the call asynchronously instead of on a fixed timer.
const amlAutoApproveDelay = 3 * time.Second

type ProviderServiceImplementation struct {
	networkClient       paymentconnect.NetworkServiceClient
	paymentStore        localpayment.Store
	settlementStore     settlement.Store
	settlementNotifier  settlement.Notifier
	amlNotifier         localpayment.AMLNotifier
	lastLookTolerance   float64 // percentage, e.g. 1.0 = 1%
	amlAutoApprove      bool
}

func NewProviderServiceImplementation(
	networkClient paymentconnect.NetworkServiceClient,
	paymentStore localpayment.Store,
	settlementStore settlement.Store,
	settlementNotifier settlement.Notifier,
	lastLookTolerance float64,
	amlAutoApprove bool,
	amlNotifier localpayment.AMLNotifier,
) *ProviderServiceImplementation {
	if lastLookTolerance < 0 {
		lastLookTolerance = 0
	}
	if settlementNotifier == nil {
		settlementNotifier = settlement.NewNoOpNotifier()
	}
	if amlNotifier == nil {
		amlNotifier = localpayment.NewNoOpNotifier()
	}
	return &ProviderServiceImplementation{
		networkClient:      networkClient,
		paymentStore:       paymentStore,
		settlementStore:    settlementStore,
		settlementNotifier: settlementNotifier,
		amlNotifier:        amlNotifier,
		lastLookTolerance:  lastLookTolerance,
		amlAutoApprove:     amlAutoApprove,
	}
}

var _ paymentconnect.ProviderServiceHandler = (*ProviderServiceImplementation)(nil)

// PayOut is invoked by the network to instruct the provider to execute a
// payout to the recipient.
func (s *ProviderServiceImplementation) PayOut(
	ctx context.Context, req *connect.Request[payment.PayoutRequest],
) (*connect.Response[payment.PayoutResponse], error) {
	paymentID := req.Msg.PaymentId
	p, err := s.paymentStore.GetByPaymentID(ctx, paymentID)
	if err != nil && !errors.Is(err, localpayment.ErrNotFound) {
		log.Printf("PayOut: failed to load payment_id=%d: %s\n", paymentID, err.Error())
		return nil, err
	}

	if p == nil {
		// Payment was created elsewhere or not yet persisted; create a placeholder.
		payoutAmount := fromSDKDecimal(req.Msg.Amount)
		var payoutAmountJSON, travelRuleJSON string
		if req.Msg.PayoutDetails != nil {
			b, _ := json.Marshal(req.Msg.PayoutDetails)
			payoutAmountJSON = string(b)
		}
		if req.Msg.TravelRuleData != nil {
			b, _ := json.Marshal(req.Msg.TravelRuleData)
			travelRuleJSON = string(b)
		}
		p = &localpayment.Payment{
			Role:               localpayment.RoleProvider,
			Status:             localpayment.StatusCreated,
			PayoutCurrency:     req.Msg.Currency,
			PayoutMethod:       methodFromDetails(req.Msg.PayoutDetails),
			PayoutAmount:       payoutAmount,
			PaymentDetailsJSON: payoutAmountJSON,
			TravelRuleDataJSON: travelRuleJSON,
		}
		id, createErr := s.paymentStore.Create(ctx, *p)
		if createErr != nil {
			log.Printf("PayOut: failed to create payment_id=%d: %s\n", paymentID, createErr.Error())
			return nil, createErr
		}
		p.ID = id
	}

	if err := s.paymentStore.UpdatePayoutRequest(ctx, p.ID, paymentID, req.Msg.PayInProviderId); err != nil {
		log.Printf("PayOut: failed to update payment_id=%d: %s\n", paymentID, err.Error())
		return nil, err
	}

	if err := s.paymentStore.UpdateManualAmlCheck(ctx, p.ID); err != nil {
		log.Printf("PayOut: failed to set MANUAL_AML_CHECK for payment_id=%d: %s\n", paymentID, err.Error())
		return nil, err
	}

	p.Status = localpayment.StatusManualAmlCheck
	if s.amlAutoApprove {
		go s.approveAmlAfter(paymentID, amlAutoApproveDelay)
	} else if notifyErr := s.amlNotifier.ManualAmlCheckRequired(ctx, *p); notifyErr != nil {
		log.Printf("PayOut: AML notification failed for payment_id=%d: %s\n", paymentID, notifyErr.Error())
	}

	log.Printf(
		"PayOut received: payment_id=%d currency=%s amount=%s client_quote_id=%s pay_in_provider_id=%d\n",
		paymentID,
		req.Msg.Currency,
		req.Msg.Amount,
		req.Msg.ClientQuoteId,
		req.Msg.PayInProviderId,
	)

	return connect.NewResponse(&payment.PayoutResponse{
		Result: &payment.PayoutResponse_ManualAmlCheck_{},
	}), nil
}

// approveAmlAfter sleeps for the given delay then calls
// CompleteManualAmlCheck(Approved). Errors are logged but not retried.
func (s *ProviderServiceImplementation) approveAmlAfter(paymentID uint64, delay time.Duration) {
	time.Sleep(delay)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := s.networkClient.CompleteManualAmlCheck(ctx, connect.NewRequest(&payment.CompleteManualAmlCheckRequest{
		PaymentId: paymentID,
		Result: &payment.CompleteManualAmlCheckRequest_Approved_{
			Approved: &payment.CompleteManualAmlCheckRequest_Approved{},
		},
	}))
	if err != nil {
		log.Printf("CompleteManualAmlCheck(Approved) for payment_id=%d failed: %s\n", paymentID, err.Error())
		return
	}
	log.Printf("CompleteManualAmlCheck(Approved) sent for payment_id=%d\n", paymentID)
}

// UpdatePayment is invoked by the network with state changes for a payment.
func (s *ProviderServiceImplementation) UpdatePayment(
	ctx context.Context, req *connect.Request[payment.UpdatePaymentRequest],
) (*connect.Response[payment.UpdatePaymentResponse], error) {
	paymentID := req.Msg.PaymentId
	p, err := s.paymentStore.GetByPaymentID(ctx, paymentID)
	if err != nil {
		if errors.Is(err, localpayment.ErrNotFound) {
			log.Printf("UpdatePayment: payment_id=%d not found in local store\n", paymentID)
			return connect.NewResponse(&payment.UpdatePaymentResponse{}), nil
		}
		return nil, err
	}

	switch result := req.Msg.Result.(type) {
	case *payment.UpdatePaymentRequest_Accepted_:
		acc := result.Accepted
		switch p.Status {
		case localpayment.StatusCreated:
			if err := s.paymentStore.UpdateAccepted(ctx, p.ID, fromSDKDecimal(acc.PayoutAmount)); err != nil {
				log.Printf("UpdatePayment Accepted: failed to update payment_id=%d: %s\n", paymentID, err.Error())
			}
		case localpayment.StatusAmlApproved:
			// Network accepted the payment after AML approval; move to quote confirmed.
			if err := s.paymentStore.UpdateStatus(ctx, p.ID, localpayment.StatusQuoteConfirmed); err != nil {
				log.Printf("UpdatePayment Accepted: failed to update status for payment_id=%d: %s\n", paymentID, err.Error())
			}
		case localpayment.StatusQuoteConfirmed:
			// Already confirmed; no state change needed.
		default:
			log.Printf("UpdatePayment Accepted: unexpected current status %s for payment_id=%d\n", p.Status, paymentID)
		}
	case *payment.UpdatePaymentRequest_Confirmed_:
		conf := result.Confirmed
		receipt := ""
		if conf.Receipt != nil {
			b, _ := json.Marshal(conf.Receipt)
			receipt = string(b)
		}
		if err := s.paymentStore.UpdateConfirmed(ctx, p.ID, "", receipt); err != nil {
			log.Printf("UpdatePayment Confirmed: failed to update payment_id=%d: %s\n", paymentID, err.Error())
		}
	case *payment.UpdatePaymentRequest_Failed_:
		failed := result.Failed
		reason := failed.Reason.String()
		if failed.Details != nil {
			reason += ": " + *failed.Details
		}
		if err := s.paymentStore.UpdateFailed(ctx, p.ID, reason); err != nil {
			log.Printf("UpdatePayment Failed: failed to update payment_id=%d: %s\n", paymentID, err.Error())
		}
	case *payment.UpdatePaymentRequest_ManualAmlCheck_:
		// No state change; the provider already returned ManualAmlCheck.
	}

	log.Printf(
		"UpdatePayment received: payment_id=%d payment_client_id=%s result=%T\n",
		paymentID, req.Msg.PaymentClientId, req.Msg.Result,
	)

	return connect.NewResponse(&payment.UpdatePaymentResponse{}), nil
}

func (s *ProviderServiceImplementation) UpdateLimit(
	ctx context.Context, req *connect.Request[payment.UpdateLimitRequest],
) (*connect.Response[payment.UpdateLimitResponse], error) {
	if s.settlementStore != nil {
		h := settlement.NewHandler(s.settlementStore, s.settlementNotifier)
		if err := h.UpdateLimit(ctx, req.Msg); err != nil {
			log.Printf("UpdateLimit failed: %s\n", err.Error())
			return nil, err
		}
	}
	return connect.NewResponse(&payment.UpdateLimitResponse{}), nil
}

func (s *ProviderServiceImplementation) AppendLedgerEntries(
	ctx context.Context, req *connect.Request[payment.AppendLedgerEntriesRequest],
) (*connect.Response[payment.AppendLedgerEntriesResponse], error) {
	if s.settlementStore != nil {
		h := settlement.NewHandler(s.settlementStore, s.settlementNotifier)
		if err := h.AppendLedgerEntries(ctx, req.Msg); err != nil {
			log.Printf("AppendLedgerEntries failed: %s\n", err.Error())
			return nil, err
		}
	}
	return connect.NewResponse(&payment.AppendLedgerEntriesResponse{}), nil
}

func (s *ProviderServiceImplementation) ApprovePaymentQuotes(ctx context.Context, c *connect.Request[payment.ApprovePaymentQuoteRequest]) (*connect.Response[payment.ApprovePaymentQuoteResponse], error) {
	req := c.Msg

	approved := true
	reason := ""

	p, err := s.paymentStore.GetByPaymentID(ctx, req.PaymentId)
	if err == nil && p != nil {
		if err := s.paymentStore.UpdateQuoteConfirmed(ctx, p.ID, fromSDKDecimal(req.PayOutAmount), fromSDKDecimal(req.SettlementAmount), req.PayOutQuoteId); err != nil {
			log.Printf("ApprovePaymentQuotes: failed to persist confirmed amounts for payment_id=%d: %s\n", req.PaymentId, err.Error())
		}

		if s.outsideTolerance(p.PayoutAmount, fromSDKDecimal(req.PayOutAmount)) {
			approved = false
			reason = fmt.Sprintf("pay-out amount outside tolerance: stored %v, got %v", p.PayoutAmount, req.PayOutAmount)
		}
		if approved && s.outsideTolerance(p.SettlementAmount, fromSDKDecimal(req.SettlementAmount)) {
			approved = false
			reason = fmt.Sprintf("settlement amount outside tolerance: stored %v, got %v", p.SettlementAmount, req.SettlementAmount)
		}

		p, _ = s.paymentStore.GetByPaymentID(ctx, req.PaymentId)
		if p != nil {
			if approved {
				if notifyErr := s.amlNotifier.QuoteConfirmed(ctx, *p); notifyErr != nil {
					log.Printf("ApprovePaymentQuotes: quote confirmed notification failed for payment_id=%d: %s\n", req.PaymentId, notifyErr.Error())
				}
			} else {
				if notifyErr := s.amlNotifier.QuoteRejected(ctx, *p, reason); notifyErr != nil {
					log.Printf("ApprovePaymentQuotes: quote rejected notification failed for payment_id=%d: %s\n", req.PaymentId, notifyErr.Error())
				}
			}
		}
	} else if err != nil && !errors.Is(err, localpayment.ErrNotFound) {
		log.Printf("ApprovePaymentQuotes: failed to load payment_id=%d: %s\n", req.PaymentId, err.Error())
	}

	var resp *payment.ApprovePaymentQuoteResponse
	if approved {
		resp = &payment.ApprovePaymentQuoteResponse{
			Result: &payment.ApprovePaymentQuoteResponse_Accepted_{Accepted: &payment.ApprovePaymentQuoteResponse_Accepted{}},
		}
		log.Printf("ApprovePaymentQuotes approved: payment_id=%d\n", req.PaymentId)
	} else {
		resp = &payment.ApprovePaymentQuoteResponse{
			Result: &payment.ApprovePaymentQuoteResponse_Rejected_{Rejected: &payment.ApprovePaymentQuoteResponse_Rejected{}},
		}
		log.Printf("ApprovePaymentQuotes rejected: payment_id=%d reason=%s\n", req.PaymentId, reason)
	}

	return connect.NewResponse(resp), nil
}

// outsideTolerance reports whether the actual decimal differs from the expected
// decimal by more than lastLookTolerance percent. Missing values are ignored.
func (s *ProviderServiceImplementation) outsideTolerance(expected, actual *localpayment.Decimal) bool {
	if expected == nil || actual == nil {
		return false
	}
	exp := decimalToFloat64(expected)
	act := decimalToFloat64(actual)
	if exp == 0 {
		return act != 0
	}
	return math.Abs(act-exp)/math.Abs(exp)*100 > s.lastLookTolerance
}

func decimalToFloat64(d *localpayment.Decimal) float64 {
	if d == nil {
		return 0
	}
	return float64(d.Unscaled) * math.Pow(10, float64(d.Exponent))
}

func fromSDKDecimal(d *common.Decimal) *localpayment.Decimal {
	if d == nil {
		return nil
	}
	return &localpayment.Decimal{Unscaled: d.Unscaled, Exponent: d.Exponent}
}

func methodFromDetails(details *common.PaymentDetails) string {
	if details == nil {
		return ""
	}
	switch details.Details.(type) {
	case *common.PaymentDetails_Sepa_:
		return "PAYMENT_METHOD_TYPE_SEPA"
	case *common.PaymentDetails_Swift_:
		return "PAYMENT_METHOD_TYPE_SWIFT"
	case *common.PaymentDetails_Fps_:
		return "PAYMENT_METHOD_TYPE_FPS"
	case *common.PaymentDetails_Ach_:
		return "PAYMENT_METHOD_TYPE_ACH"
	default:
		return ""
	}
}

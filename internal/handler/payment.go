package handler

import (
	"context"
	"log"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
)

// amlAutoApproveDelay is how long the provider waits after returning
// ManualAmlCheck before calling CompleteManualAmlCheck(Approved).
// This is the Phase 2 smoke-test value; real AML processing should
// drive the call asynchronously instead of on a fixed timer.
const amlAutoApproveDelay = 3 * time.Second

// payments tracks PayoutRequests that returned ManualAmlCheck so the
// provider's state can be observed across callbacks (e.g. via UpdatePayment
// logs). Process-local map; resets on restart.
var payments sync.Map // map[uint64]time.Time — payment_id → received_at

type ProviderServiceImplementation struct {
	networkClient paymentconnect.NetworkServiceClient
}

func NewProviderServiceImplementation(networkClient paymentconnect.NetworkServiceClient) *ProviderServiceImplementation {
	return &ProviderServiceImplementation{
		networkClient: networkClient,
	}
}

/*
  Please refer to docs, proto definition comments or source code comments to understand purpose of functions
*/

var _ paymentconnect.ProviderServiceHandler = (*ProviderServiceImplementation)(nil)

// PayOut is invoked by the network to instruct the provider to execute a
// payout to the recipient. Phase 2 smoke-test behaviour:
//   - Return ManualAmlCheck so the network enters the manual-AML branch.
//   - After a short delay, call CompleteManualAmlCheck(Approved) so the
//     network advances the payment to UpdatePayment(Accepted).
//
// The delayed RPC runs on a fresh context (not the request ctx) so it
// survives after the handler returns.
func (s *ProviderServiceImplementation) PayOut(
	ctx context.Context, req *connect.Request[payment.PayoutRequest],
) (*connect.Response[payment.PayoutResponse], error) {
	paymentID := req.Msg.PaymentId
	receivedAt := time.Now()
	payments.Store(paymentID, receivedAt)

	log.Printf(
		"PayOut received: payment_id=%d currency=%s amount=%s method=%v client_quote_id=%s pay_in_provider_id=%d\n",
		paymentID,
		req.Msg.Currency,
		req.Msg.Amount,
		req.Msg.PayoutDetails,
		req.Msg.ClientQuoteId,
		req.Msg.PayInProviderId,
	)

	go s.approveAmlAfter(paymentID, amlAutoApproveDelay)

	return connect.NewResponse(&payment.PayoutResponse{
		Result: &payment.PayoutResponse_ManualAmlCheck_{},
	}), nil
}

// approveAmlAfter sleeps for the given delay then calls
// CompleteManualAmlCheck(Approved). Errors are logged but not retried —
// the network is idempotent and will retry PayoutRequest if needed.
func (s *ProviderServiceImplementation) approveAmlAfter(paymentID uint64, delay time.Duration) {
	time.Sleep(delay)

	// Detached context so we outlive the originating request.
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

// UpdatePayment is invoked by the network with state changes for a payment
// previously submitted via PayOut or equivalent flow.
func (s *ProviderServiceImplementation) UpdatePayment(
	ctx context.Context, req *connect.Request[payment.UpdatePaymentRequest],
) (*connect.Response[payment.UpdatePaymentResponse], error) {
	resultKind := "unknown"
	switch req.Msg.Result.(type) {
	case *payment.UpdatePaymentRequest_Accepted_:
		resultKind = "Accepted"
	case *payment.UpdatePaymentRequest_Failed_:
		resultKind = "Failed"
	case *payment.UpdatePaymentRequest_Confirmed_:
		resultKind = "Confirmed"
	case *payment.UpdatePaymentRequest_ManualAmlCheck_:
		resultKind = "ManualAmlCheck"
	}

	if v, ok := payments.Load(req.Msg.PaymentId); ok {
		log.Printf(
			"UpdatePayment received: payment_id=%d payment_client_id=%s result=%s (PayoutRequest was received %s ago)\n",
			req.Msg.PaymentId, req.Msg.PaymentClientId, resultKind, time.Since(v.(time.Time)).Round(time.Millisecond),
		)
	} else {
		log.Printf(
			"UpdatePayment received: payment_id=%d payment_client_id=%s result=%s (no prior PayoutRequest in this process)\n",
			req.Msg.PaymentId, req.Msg.PaymentClientId, resultKind,
		)
	}

	return connect.NewResponse(&payment.UpdatePaymentResponse{}), nil
}

func (s *ProviderServiceImplementation) UpdateLimit(
	ctx context.Context, req *connect.Request[payment.UpdateLimitRequest],
) (*connect.Response[payment.UpdateLimitResponse], error) {
	// TODO: optionally implement handling of the notifications about updates on your limits and limits usage
	return connect.NewResponse(&payment.UpdateLimitResponse{}), nil
}

func (s *ProviderServiceImplementation) AppendLedgerEntries(
	ctx context.Context, req *connect.Request[payment.AppendLedgerEntriesRequest],
) (*connect.Response[payment.AppendLedgerEntriesResponse], error) {
	// TODO: optionally implement handling of the notifications about new ledger transactions and new ledger entries
	return connect.NewResponse(&payment.AppendLedgerEntriesResponse{}), nil
}

func (s *ProviderServiceImplementation) ApprovePaymentQuotes(ctx context.Context, c *connect.Request[payment.ApprovePaymentQuoteRequest]) (*connect.Response[payment.ApprovePaymentQuoteResponse], error) {
	//TODO: this is the endpoint to have a last look at quote and approve after AML check is done
	return connect.NewResponse(&payment.ApprovePaymentQuoteResponse{}), nil
}
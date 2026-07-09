package internal

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"testing"

	"connectrpc.com/connect"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
)

type getQuoteNetwork struct {
	resp *payment.GetQuoteResponse
	err  error
}

func (n *getQuoteNetwork) UpdateQuote(_ context.Context, _ *connect.Request[payment.UpdateQuoteRequest]) (*connect.Response[payment.UpdateQuoteResponse], error) {
	return nil, nil
}

func (n *getQuoteNetwork) GetQuote(_ context.Context, _ *connect.Request[payment.GetQuoteRequest]) (*connect.Response[payment.GetQuoteResponse], error) {
	if n.err != nil {
		return nil, n.err
	}
	return connect.NewResponse(n.resp), nil
}

func (n *getQuoteNetwork) CreatePayment(_ context.Context, _ *connect.Request[payment.CreatePaymentRequest]) (*connect.Response[payment.CreatePaymentResponse], error) {
	return nil, nil
}

//nolint:staticcheck // Required to satisfy paymentconnect.NetworkServiceClient interface.
func (n *getQuoteNetwork) ConfirmPayout(_ context.Context, _ *connect.Request[payment.ConfirmPayoutRequest]) (*connect.Response[payment.ConfirmPayoutResponse], error) {
	return nil, nil
}

func (n *getQuoteNetwork) FinalizePayout(_ context.Context, _ *connect.Request[payment.FinalizePayoutRequest]) (*connect.Response[payment.FinalizePayoutResponse], error) {
	return nil, nil
}

func (n *getQuoteNetwork) CompleteManualAmlCheck(_ context.Context, _ *connect.Request[payment.CompleteManualAmlCheckRequest]) (*connect.Response[payment.CompleteManualAmlCheckResponse], error) {
	return nil, nil
}

var _ paymentconnect.NetworkServiceClient = (*getQuoteNetwork)(nil)

func TestGetQuote_Success(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	net := &getQuoteNetwork{
		resp: &payment.GetQuoteResponse{
			Result: &payment.GetQuoteResponse_Success_{Success: &payment.GetQuoteResponse_Success{
				QuoteId: &payment.QuoteId{QuoteId: 123},
			}},
		},
	}

	GetQuote(context.Background(), net)

	if !bytes.Contains(buf.Bytes(), []byte("Got success response")) {
		t.Errorf("expected success log, got: %s", buf.String())
	}
}

func TestGetQuote_Failure(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	net := &getQuoteNetwork{
		resp: &payment.GetQuoteResponse{
			Result: &payment.GetQuoteResponse_Failure_{Failure: &payment.GetQuoteResponse_Failure{
				Reason: payment.GetQuoteResponse_Failure_REASON_UNSPECIFIED,
			}},
		},
	}

	GetQuote(context.Background(), net)

	if !bytes.Contains(buf.Bytes(), []byte("Got failure response")) {
		t.Errorf("expected failure log, got: %s", buf.String())
	}
}

func TestGetQuote_NetworkError(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	net := &getQuoteNetwork{err: errors.New("network error")}

	GetQuote(context.Background(), net)

	if !bytes.Contains(buf.Bytes(), []byte("Error getting quote")) {
		t.Errorf("expected error log, got: %s", buf.String())
	}
}

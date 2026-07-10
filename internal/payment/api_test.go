package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"connectrpc.com/connect"
	sdkpayment "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
)

type fakePaymentNetwork struct {
	createdPaymentID uint64
	amlApproved      []uint64
	amlRejected      []uint64
	finalized        []uint64
}

func (f *fakePaymentNetwork) UpdateQuote(_ context.Context, _ *connect.Request[sdkpayment.UpdateQuoteRequest]) (*connect.Response[sdkpayment.UpdateQuoteResponse], error) {
	return nil, nil
}

func (f *fakePaymentNetwork) GetQuote(_ context.Context, _ *connect.Request[sdkpayment.GetQuoteRequest]) (*connect.Response[sdkpayment.GetQuoteResponse], error) {
	return nil, nil
}

func (f *fakePaymentNetwork) CreatePayment(_ context.Context, _ *connect.Request[sdkpayment.CreatePaymentRequest]) (*connect.Response[sdkpayment.CreatePaymentResponse], error) {
	return connect.NewResponse(&sdkpayment.CreatePaymentResponse{
		Result: &sdkpayment.CreatePaymentResponse_Accepted_{
			Accepted: &sdkpayment.CreatePaymentResponse_Accepted{
				PaymentId:        f.createdPaymentID,
				PayoutProviderId: 7,
			},
		},
	}), nil
}

//nolint:staticcheck // Required to satisfy paymentconnect.NetworkServiceClient interface.
func (f *fakePaymentNetwork) ConfirmPayout(_ context.Context, _ *connect.Request[sdkpayment.ConfirmPayoutRequest]) (*connect.Response[sdkpayment.ConfirmPayoutResponse], error) {
	return nil, nil
}

func (f *fakePaymentNetwork) FinalizePayout(_ context.Context, _ *connect.Request[sdkpayment.FinalizePayoutRequest]) (*connect.Response[sdkpayment.FinalizePayoutResponse], error) {
	f.finalized = append(f.finalized, 0) // value filled by caller if needed
	return connect.NewResponse(&sdkpayment.FinalizePayoutResponse{}), nil
}

func (f *fakePaymentNetwork) CompleteManualAmlCheck(_ context.Context, req *connect.Request[sdkpayment.CompleteManualAmlCheckRequest]) (*connect.Response[sdkpayment.CompleteManualAmlCheckResponse], error) {
	switch req.Msg.Result.(type) {
	case *sdkpayment.CompleteManualAmlCheckRequest_Approved_:
		f.amlApproved = append(f.amlApproved, req.Msg.PaymentId)
	case *sdkpayment.CompleteManualAmlCheckRequest_Rejected_:
		f.amlRejected = append(f.amlRejected, req.Msg.PaymentId)
	}
	return connect.NewResponse(&sdkpayment.CompleteManualAmlCheckResponse{}), nil
}

var _ paymentconnect.NetworkServiceClient = (*fakePaymentNetwork)(nil)

func newTestAPIHandler(t *testing.T) (*Handler, *fakePaymentNetwork, Store) {
	t.Helper()
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	net := &fakePaymentNetwork{createdPaymentID: 123}
	client := NewNetworkClient(net)
	return NewHandler(store, client, []string{"test-key"}), net, store
}

func TestAPI_CreatePayment_SetsOFIRole(t *testing.T) {
	h, _, _ := newTestAPIHandler(t)

	body := `{
		"paymentClientId": "client-1",
		"amount": {"unscaled": 1000, "exponent": 0},
		"amountType": "pay_out",
		"currency": "GBP",
		"paymentMethod": "PAYMENT_METHOD_TYPE_SWIFT",
		"paymentDetails": {"accountNumber":"123","swiftCode":"ABC","beneficiaryName":"Bob"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var p Payment
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if p.Role != RoleOFI {
		t.Errorf("expected role %s, got %s", RoleOFI, p.Role)
	}
	if p.PaymentID == nil || *p.PaymentID != 123 {
		t.Errorf("expected paymentID 123, got %v", p.PaymentID)
	}
}

func TestAPI_AmlApprove(t *testing.T) {
	h, net, store := newTestAPIHandler(t)

	id, err := store.Create(context.Background(), Payment{
		PaymentClientID: "client-aml",
		Role:            RoleProvider,
		Status:          StatusPayoutRequested,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(99)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/"+formatInt(id)+"/aml/approve", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(net.amlApproved) != 1 || net.amlApproved[0] != 99 {
		t.Errorf("expected AML approve for 99, got %v", net.amlApproved)
	}
}

func TestAPI_AmlReject(t *testing.T) {
	h, net, store := newTestAPIHandler(t)

	id, err := store.Create(context.Background(), Payment{
		PaymentClientID: "client-aml-reject",
		Role:            RoleProvider,
		Status:          StatusPayoutRequested,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(101)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	body := `{"reason": "suspicious"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/"+formatInt(id)+"/aml/reject", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(net.amlRejected) != 1 || net.amlRejected[0] != 101 {
		t.Errorf("expected AML reject for 101, got %v", net.amlRejected)
	}
	p, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if p.Status != StatusFailed {
		t.Errorf("expected status %s, got %s", StatusFailed, p.Status)
	}
}

func TestAPI_Unauthorized(t *testing.T) {
	h, _, _ := newTestAPIHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/1", nil)
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func ptr[T any](v T) *T { return &v }
func formatInt(i int64) string {
	var buf [32]byte
	return string(strconv.AppendInt(buf[:0], i, 10))
}

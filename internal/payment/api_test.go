package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"connectrpc.com/connect"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	sdkpayment "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
)

type fakePaymentNetwork struct {
	createdPaymentID uint64
	amlApproved      []uint64
	amlRejected      []uint64
	finalized        []uint64
	finalizeErr      error
	createErr        error
	createFailure    bool
}

func (f *fakePaymentNetwork) UpdateQuote(_ context.Context, _ *connect.Request[sdkpayment.UpdateQuoteRequest]) (*connect.Response[sdkpayment.UpdateQuoteResponse], error) {
	return nil, nil
}

func (f *fakePaymentNetwork) GetQuote(_ context.Context, _ *connect.Request[sdkpayment.GetQuoteRequest]) (*connect.Response[sdkpayment.GetQuoteResponse], error) {
	return nil, nil
}

func (f *fakePaymentNetwork) CreatePayment(_ context.Context, _ *connect.Request[sdkpayment.CreatePaymentRequest]) (*connect.Response[sdkpayment.CreatePaymentResponse], error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.createFailure {
		return connect.NewResponse(&sdkpayment.CreatePaymentResponse{
			Result: &sdkpayment.CreatePaymentResponse_Failure_{Failure: &sdkpayment.CreatePaymentResponse_Failure{}},
		}), nil
	}
	return connect.NewResponse(&sdkpayment.CreatePaymentResponse{
		Result: &sdkpayment.CreatePaymentResponse_Accepted_{
			Accepted: &sdkpayment.CreatePaymentResponse_Accepted{
				PaymentId:        f.createdPaymentID,
				PayoutProviderId: 7,
				PayoutAmount:     &common.Decimal{Unscaled: 1000, Exponent: 0},
			},
		},
	}), nil
}

//nolint:staticcheck // Required to satisfy paymentconnect.NetworkServiceClient interface.
func (f *fakePaymentNetwork) ConfirmPayout(_ context.Context, _ *connect.Request[sdkpayment.ConfirmPayoutRequest]) (*connect.Response[sdkpayment.ConfirmPayoutResponse], error) {
	return nil, nil
}

func (f *fakePaymentNetwork) FinalizePayout(_ context.Context, req *connect.Request[sdkpayment.FinalizePayoutRequest]) (*connect.Response[sdkpayment.FinalizePayoutResponse], error) {
	if f.finalizeErr != nil {
		return nil, f.finalizeErr
	}
	f.finalized = append(f.finalized, req.Msg.PaymentId)
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

func newTestAPIHandlerWithAMLAdmins(t *testing.T) (*Handler, *fakePaymentNetwork, Store) {
	t.Helper()
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	net := &fakePaymentNetwork{createdPaymentID: 123}
	client := NewNetworkClient(net)
	admins := map[string]struct{}{"aml-admin": {}}
	return NewHandlerWithAMLAdmins(store, client, []string{"test-key", "aml-admin"}, admins), net, store
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

func TestAPI_ListPayments(t *testing.T) {
	h, _, store := newTestAPIHandler(t)

	ctx := context.Background()
	_, err := store.Create(ctx, Payment{
		PaymentClientID: "client-list-1",
		Role:            RoleOFI,
		Status:          StatusCreated,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err = store.Create(ctx, Payment{
		PaymentClientID: "client-list-2",
		Role:            RoleProvider,
		Status:          StatusManualAmlCheck,
		PayoutCurrency:  "EUR",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SEPA",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments?role=provider&status=MANUAL_AML_CHECK", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var out []Payment
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 payment, got %d", len(out))
	}
	if out[0].Role != RoleProvider {
		t.Errorf("expected role provider, got %s", out[0].Role)
	}
	if out[0].Status != StatusManualAmlCheck {
		t.Errorf("expected status MANUAL_AML_CHECK, got %s", out[0].Status)
	}
}

func TestAPI_AmlApprove(t *testing.T) {
	h, net, store := newTestAPIHandler(t)

	id, err := store.Create(context.Background(), Payment{
		PaymentClientID: "client-aml",
		Role:            RoleProvider,
		Status:          StatusManualAmlCheck,
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
	p, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if p.Status != StatusAmlApproved {
		t.Errorf("expected status %s, got %s", StatusAmlApproved, p.Status)
	}
	if p.AmlDecisionBy == "" {
		t.Error("expected aml_decision_by to be set")
	}
}

func TestAPI_AmlApprove_Idempotent(t *testing.T) {
	h, net, store := newTestAPIHandler(t)

	id, err := store.Create(context.Background(), Payment{
		PaymentClientID: "client-aml-idempotent",
		Role:            RoleProvider,
		Status:          StatusAmlApproved,
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
	if len(net.amlApproved) != 0 {
		t.Errorf("expected no network call on idempotent approve, got %v", net.amlApproved)
	}
}

func TestAPI_AmlReject(t *testing.T) {
	h, net, store := newTestAPIHandler(t)

	id, err := store.Create(context.Background(), Payment{
		PaymentClientID: "client-aml-reject",
		Role:            RoleProvider,
		Status:          StatusManualAmlCheck,
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
	if p.RejectReason != "suspicious" {
		t.Errorf("expected reject reason suspicious, got %s", p.RejectReason)
	}
}

func TestAPI_AmlReject_RequiresReason(t *testing.T) {
	h, net, store := newTestAPIHandler(t)

	id, err := store.Create(context.Background(), Payment{
		PaymentClientID: "client-aml-reject-no-reason",
		Role:            RoleProvider,
		Status:          StatusManualAmlCheck,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(102)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/"+formatInt(id)+"/aml/reject", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if len(net.amlRejected) != 0 {
		t.Errorf("expected no network call without reason, got %v", net.amlRejected)
	}
}

func TestAPI_AmlDecision_RequiresAdminKey(t *testing.T) {
	h, _, store := newTestAPIHandlerWithAMLAdmins(t)

	id, err := store.Create(context.Background(), Payment{
		PaymentClientID: "client-aml-admin",
		Role:            RoleProvider,
		Status:          StatusManualAmlCheck,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(103)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/"+formatInt(id)+"/aml/approve", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/payments/"+formatInt(id)+"/aml/approve", nil)
	req.Header.Set("Authorization", "Bearer aml-admin")
	w = httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_AmlApprove_OperatorID(t *testing.T) {
	h, _, store := newTestAPIHandler(t)

	id, err := store.Create(context.Background(), Payment{
		PaymentClientID: "client-aml-operator",
		Role:            RoleProvider,
		Status:          StatusManualAmlCheck,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(104)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	body := `{"operatorId": "operator-42"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/"+formatInt(id)+"/aml/approve", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	p, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if p.AmlDecisionBy != "operator-42" {
		t.Errorf("expected operator operator-42, got %s", p.AmlDecisionBy)
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

func TestAPI_AmlApprove_InvalidPaymentID(t *testing.T) {
	h, _, _ := newTestAPIHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/not-a-number/aml/approve", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestAPI_AmlApprove_MissingNetworkPaymentID(t *testing.T) {
	h, _, store := newTestAPIHandler(t)

	id, err := store.Create(context.Background(), Payment{
		PaymentClientID: "client-no-network-id",
		Role:            RoleProvider,
		Status:          StatusManualAmlCheck,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/"+formatInt(id)+"/aml/approve", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_AmlDecision_InvalidJSON(t *testing.T) {
	h, _, store := newTestAPIHandler(t)

	id, err := store.Create(context.Background(), Payment{
		PaymentClientID: "client-invalid-json",
		Role:            RoleProvider,
		Status:          StatusManualAmlCheck,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(105)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/"+formatInt(id)+"/aml/approve", bytes.NewReader([]byte("not json")))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_Auth_InvalidPrefix(t *testing.T) {
	h, _, _ := newTestAPIHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/1", nil)
	req.Header.Set("Authorization", "Basic test-key")
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

func TestAPI_GetPayment(t *testing.T) {
	h, _, store := newTestAPIHandler(t)

	id, err := store.Create(context.Background(), Payment{
		PaymentClientID: "client-get",
		Role:            RoleOFI,
		Status:          StatusCreated,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/"+formatInt(id), nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var p Payment
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if p.ID != id {
		t.Errorf("expected id %d, got %d", id, p.ID)
	}
}

func TestAPI_GetPayment_NotFound(t *testing.T) {
	h, _, _ := newTestAPIHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/9999", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_GetPayment_InvalidID(t *testing.T) {
	h, _, _ := newTestAPIHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/not-a-number", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_FinalizePayment(t *testing.T) {
	h, net, store := newTestAPIHandler(t)

	id, err := store.Create(context.Background(), Payment{
		PaymentClientID: "client-finalize",
		Role:            RoleProvider,
		Status:          StatusQuoteConfirmed,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(201)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	body := `{"success": true, "payoutId": "payout-201", "receipt": "receipt-201"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/"+formatInt(id)+"/finalize", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(net.finalized) != 1 || net.finalized[0] != 201 {
		t.Errorf("expected finalize for 201, got %v", net.finalized)
	}
	var p Payment
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if p.Status != StatusPayoutAccepted {
		t.Errorf("expected status %s, got %s", StatusPayoutAccepted, p.Status)
	}
	if p.PayoutID != "payout-201" {
		t.Errorf("expected payout id payout-201, got %s", p.PayoutID)
	}
}

func TestAPI_FinalizePayment_NotFound(t *testing.T) {
	h, _, _ := newTestAPIHandler(t)

	body := `{"success": true, "payoutId": "x", "receipt": "y"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/9999/finalize", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_FinalizePayment_MissingNetworkID(t *testing.T) {
	h, _, store := newTestAPIHandler(t)

	id, err := store.Create(context.Background(), Payment{
		PaymentClientID: "client-finalize-no-network-id",
		Role:            RoleProvider,
		Status:          StatusQuoteConfirmed,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	body := `{"success": true, "payoutId": "x", "receipt": "y"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/"+formatInt(id)+"/finalize", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_FinalizePayment_NetworkError(t *testing.T) {
	h, net, store := newTestAPIHandler(t)
	net.finalizeErr = errors.New("finalize failed")

	id, err := store.Create(context.Background(), Payment{
		PaymentClientID: "client-finalize-err",
		Role:            RoleProvider,
		Status:          StatusQuoteConfirmed,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(202)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	body := `{"success": true, "payoutId": "x", "receipt": "y"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/"+formatInt(id)+"/finalize", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_CreatePayment_InvalidJSON(t *testing.T) {
	h, _, _ := newTestAPIHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", bytes.NewReader([]byte("not json")))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_CreatePayment_ValidationError(t *testing.T) {
	h, _, _ := newTestAPIHandler(t)

	body := `{"paymentClientId": "", "amount": {"unscaled": 100, "exponent": 0}, "amountType": "pay_out", "currency": "GBP", "paymentMethod": "SWIFT"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_CreatePayment_DomainValidationError(t *testing.T) {
	h, _, _ := newTestAPIHandler(t)

	body := `{"paymentClientId": "client-bad", "amount": {"unscaled": 100, "exponent": 0}, "amountType": "pay_out", "currency": "gbp", "paymentMethod": "SWIFT"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_CreatePayment_NetworkError(t *testing.T) {
	h, net, _ := newTestAPIHandler(t)
	net.createErr = errors.New("network down")

	body := `{"paymentClientId": "client-network-err", "amount": {"unscaled": 100, "exponent": 0}, "amountType": "pay_out", "currency": "GBP", "paymentMethod": "SWIFT"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_CreatePayment_FailureResponse(t *testing.T) {
	h, net, _ := newTestAPIHandler(t)
	net.createFailure = true

	body := `{"paymentClientId": "client-failure", "amount": {"unscaled": 100, "exponent": 0}, "amountType": "pay_out", "currency": "GBP", "paymentMethod": "SWIFT"}`
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
	if p.Status != StatusFailed {
		t.Errorf("expected status %s, got %s", StatusFailed, p.Status)
	}
}

func TestAPI_CreatePayment_Idempotent(t *testing.T) {
	h, net, _ := newTestAPIHandler(t)

	body := `{"paymentClientId": "client-dup", "amount": {"unscaled": 100, "exponent": 0}, "amountType": "pay_out", "currency": "GBP", "paymentMethod": "SWIFT"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// Second request with same client id should return existing record.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/payments", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for idempotent create, got %d: %s", w.Code, w.Body.String())
	}
	if len(net.finalized) != 0 {
		t.Errorf("expected no finalize calls, got %v", net.finalized)
	}
}

func TestAPI_ListPayments_LimitOffset(t *testing.T) {
	h, _, store := newTestAPIHandler(t)
	ctx := context.Background()

	_, err := store.Create(ctx, Payment{PaymentClientID: "a", Role: RoleOFI, Status: StatusCreated, PayoutCurrency: "GBP", PayoutMethod: "SWIFT"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err = store.Create(ctx, Payment{PaymentClientID: "b", Role: RoleOFI, Status: StatusCreated, PayoutCurrency: "GBP", PayoutMethod: "SWIFT"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments?limit=1&offset=1", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var out []Payment
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 payment, got %d", len(out))
	}
}

func TestAPI_Auth_InvalidKey(t *testing.T) {
	h, _, _ := newTestAPIHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/1", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestAPI_AmlApprove_StoreErrorAfterNetwork(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	_ = store.Close() // closed store will error on GetByID

	net := &fakePaymentNetwork{createdPaymentID: 123}
	client := NewNetworkClient(net)
	h := NewHandler(store, client, []string{"test-key"})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/1/aml/approve", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_FinalizePayment_InvalidJSON(t *testing.T) {
	h, _, store := newTestAPIHandler(t)

	id, err := store.Create(context.Background(), Payment{
		PaymentClientID: "client-finalize-bad-json",
		Role:            RoleProvider,
		Status:          StatusQuoteConfirmed,
		PayoutCurrency:  "GBP",
		PayoutMethod:    "PAYMENT_METHOD_TYPE_SWIFT",
		PaymentID:       ptr(uint64(203)),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/"+formatInt(id)+"/finalize", bytes.NewReader([]byte("not json")))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_OperatorIDFromKey(t *testing.T) {
	if operatorIDFromKey("") != "unknown" {
		t.Errorf("expected unknown for empty key")
	}
	if operatorIDFromKey("short") != "short" {
		t.Errorf("expected short for short key")
	}
	if operatorIDFromKey("longer-key-123") != "longer-k" {
		t.Errorf("expected longer-k for longer key, got %s", operatorIDFromKey("longer-key-123"))
	}
}

// fakeStore wraps a Store and can inject errors into specific methods for coverage.
type fakeStore struct {
	Store
	getByPaymentClientIDErr error
	createErr               error
	updatePayoutRequestErr  error
	updateAcceptedErr       error
	getByIDErr              error
}

func (f *fakeStore) GetByPaymentClientID(ctx context.Context, clientID string) (*Payment, error) {
	if f.getByPaymentClientIDErr != nil {
		return nil, f.getByPaymentClientIDErr
	}
	return f.Store.GetByPaymentClientID(ctx, clientID)
}

func (f *fakeStore) Create(ctx context.Context, p Payment) (int64, error) {
	if f.createErr != nil {
		return 0, f.createErr
	}
	return f.Store.Create(ctx, p)
}

func (f *fakeStore) UpdatePayoutRequest(ctx context.Context, id int64, paymentID uint64, payoutProviderID uint32) error {
	if f.updatePayoutRequestErr != nil {
		return f.updatePayoutRequestErr
	}
	return f.Store.UpdatePayoutRequest(ctx, id, paymentID, payoutProviderID)
}

func (f *fakeStore) UpdateAccepted(ctx context.Context, id int64, payoutAmount *Decimal) error {
	if f.updateAcceptedErr != nil {
		return f.updateAcceptedErr
	}
	return f.Store.UpdateAccepted(ctx, id, payoutAmount)
}

func (f *fakeStore) GetByID(ctx context.Context, id int64) (*Payment, error) {
	if f.getByIDErr != nil {
		return nil, f.getByIDErr
	}
	return f.Store.GetByID(ctx, id)
}

func TestAPI_CreatePayment_GetByClientIDError(t *testing.T) {
	base, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer func() { _ = base.Close() }()

	store := &fakeStore{Store: base, getByPaymentClientIDErr: errors.New("db error")}
	net := &fakePaymentNetwork{createdPaymentID: 123}
	client := NewNetworkClient(net)
	h := NewHandler(store, client, []string{"test-key"})

	body := `{"paymentClientId": "client-db-err", "amount": {"unscaled": 100, "exponent": 0}, "amountType": "pay_out", "currency": "GBP", "paymentMethod": "PAYMENT_METHOD_TYPE_SWIFT"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_CreatePayment_CreateError(t *testing.T) {
	base, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer func() { _ = base.Close() }()

	store := &fakeStore{Store: base, createErr: errors.New("create failed")}
	net := &fakePaymentNetwork{createdPaymentID: 123}
	client := NewNetworkClient(net)
	h := NewHandler(store, client, []string{"test-key"})

	body := `{"paymentClientId": "client-create-err", "amount": {"unscaled": 100, "exponent": 0}, "amountType": "pay_out", "currency": "GBP", "paymentMethod": "PAYMENT_METHOD_TYPE_SWIFT"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_CreatePayment_UpdatePayoutRequestError(t *testing.T) {
	base, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer func() { _ = base.Close() }()

	store := &fakeStore{Store: base, updatePayoutRequestErr: errors.New("update failed")}
	net := &fakePaymentNetwork{createdPaymentID: 123}
	client := NewNetworkClient(net)
	h := NewHandler(store, client, []string{"test-key"})

	body := `{"paymentClientId": "client-update-err", "amount": {"unscaled": 100, "exponent": 0}, "amountType": "pay_out", "currency": "GBP", "paymentMethod": "PAYMENT_METHOD_TYPE_SWIFT"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_CreatePayment_UpdateAcceptedError(t *testing.T) {
	base, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer func() { _ = base.Close() }()

	store := &fakeStore{Store: base, updateAcceptedErr: errors.New("update accepted failed")}
	net := &fakePaymentNetwork{createdPaymentID: 123}
	client := NewNetworkClient(net)
	h := NewHandler(store, client, []string{"test-key"})

	body := `{"paymentClientId": "client-update-accepted-err", "amount": {"unscaled": 100, "exponent": 0}, "amountType": "pay_out", "currency": "GBP", "paymentMethod": "PAYMENT_METHOD_TYPE_SWIFT"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPI_CreatePayment_WithQuoteID(t *testing.T) {
	h, _, _ := newTestAPIHandler(t)

	body := `{"paymentClientId": "client-quote", "amount": {"unscaled": 100, "exponent": 0}, "amountType": "pay_out", "currency": "GBP", "paymentMethod": "PAYMENT_METHOD_TYPE_SWIFT", "quoteId": {"quoteId": 42, "providerId": 7}}`
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
	if p.QuoteID == nil || *p.QuoteID != 42 {
		t.Errorf("expected quote id 42, got %v", p.QuoteID)
	}
}

func TestAPI_FinalizePayment_GetByIDError(t *testing.T) {
	base, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer func() { _ = base.Close() }()

	store := &fakeStore{Store: base, getByIDErr: errors.New("get failed")}
	net := &fakePaymentNetwork{createdPaymentID: 123}
	client := NewNetworkClient(net)
	h := NewHandler(store, client, []string{"test-key"})

	body := `{"success": true, "payoutId": "x", "receipt": "y"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/1/finalize", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Router().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

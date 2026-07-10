package payment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/t-0-network/provider-sdk-go/api/ivms101/v1/ivms"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
)

// NetworkClient wraps the SDK NetworkServiceClient with domain-friendly methods.
type NetworkClient struct {
	client paymentconnect.NetworkServiceClient
}

// NewNetworkClient creates a new payment network client.
func NewNetworkClient(client paymentconnect.NetworkServiceClient) *NetworkClient {
	return &NetworkClient{client: client}
}

// CreatePayment submits a payment creation request to the t-0 Network.
func (c *NetworkClient) CreatePayment(ctx context.Context, req CreateRequest) (*payment.CreatePaymentResponse, error) {
	var amount *payment.PaymentAmount
	switch req.AmountType {
	case "pay_out":
		amount = &payment.PaymentAmount{Amount: &payment.PaymentAmount_PayOutAmount{
			PayOutAmount: toCommonDecimal(req.Amount),
		}}
	case "settlement":
		amount = &payment.PaymentAmount{Amount: &payment.PaymentAmount_SettlementAmount{
			SettlementAmount: toCommonDecimal(req.Amount),
		}}
	}

	sdkReq := &payment.CreatePaymentRequest{
		PaymentClientId: req.PaymentClientID,
		Amount:          amount,
		Currency:        req.Currency,
		PaymentDetails:  buildPaymentDetails(req.PaymentMethod, req.PaymentDetails),
	}

	if req.QuoteID != nil {
		sdkReq.QuoteId = &payment.QuoteId{
			QuoteId:    req.QuoteID.QuoteID,
			ProviderId: req.QuoteID.ProviderID,
		}
	}

	if len(req.TravelRuleData) > 0 {
		var tr payment.CreatePaymentRequest_TravelRuleData
		if err := json.Unmarshal(req.TravelRuleData, &tr); err != nil {
			return nil, fmt.Errorf("parsing travelRuleData: %w", err)
		}
		sdkReq.TravelRuleData = &tr
	}

	resp, err := c.client.CreatePayment(ctx, connect.NewRequest(sdkReq))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// FinalizePayout reports a payout result to the t-0 Network.
func (c *NetworkClient) FinalizePayout(ctx context.Context, paymentID uint64, req FinalizeRequest) error {
	sdkReq := &payment.FinalizePayoutRequest{PaymentId: paymentID}
	if req.Success {
		sdkReq.Result = &payment.FinalizePayoutRequest_Success_{Success: &payment.FinalizePayoutRequest_Success{}}
		if req.Receipt != "" {
			sdkReq.Result.(*payment.FinalizePayoutRequest_Success_).Success.Receipt = buildPaymentReceipt(req.Receipt)
		}
	} else {
		sdkReq.Result = &payment.FinalizePayoutRequest_Failure_{Failure: &payment.FinalizePayoutRequest_Failure{
			Reason: req.RejectReason,
		}}
	}

	_, err := c.client.FinalizePayout(ctx, connect.NewRequest(sdkReq))
	return err
}

// CompleteManualAmlCheck reports the result of a manual AML check to the t-0 Network.
func (c *NetworkClient) CompleteManualAmlCheck(ctx context.Context, paymentID uint64, approved bool, reason string) error {
	sdkReq := &payment.CompleteManualAmlCheckRequest{PaymentId: paymentID}
	if approved {
		sdkReq.Result = &payment.CompleteManualAmlCheckRequest_Approved_{Approved: &payment.CompleteManualAmlCheckRequest_Approved{}}
	} else {
		sdkReq.Result = &payment.CompleteManualAmlCheckRequest_Rejected_{Rejected: &payment.CompleteManualAmlCheckRequest_Rejected{
			Reason: reason,
		}}
	}

	_, err := c.client.CompleteManualAmlCheck(ctx, connect.NewRequest(sdkReq))
	return err
}

func toCommonDecimal(d Decimal) *common.Decimal {
	return &common.Decimal{Unscaled: d.Unscaled, Exponent: d.Exponent}
}

func fromCommonDecimal(d *common.Decimal) *Decimal {
	if d == nil {
		return nil
	}
	return &Decimal{Unscaled: d.Unscaled, Exponent: d.Exponent}
}

func buildPaymentDetails(method string, raw JSONRaw) *common.PaymentDetails {
	method = strings.ToUpper(method)
	switch method {
	case "PAYMENT_METHOD_TYPE_SEPA", "SEPA":
		var sepa struct {
			Iban             string `json:"iban"`
			BeneficiaryName  string `json:"beneficiaryName"`
			PaymentReference string `json:"paymentReference"`
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &sepa)
		}
		return &common.PaymentDetails{
			Details: &common.PaymentDetails_Sepa_{Sepa: &common.PaymentDetails_Sepa{
				Iban:             sepa.Iban,
				BeneficiaryName:  sepa.BeneficiaryName,
				PaymentReference: sepa.PaymentReference,
			}},
		}
	case "PAYMENT_METHOD_TYPE_SWIFT", "SWIFT":
		var swift struct {
			AccountNumber   string `json:"accountNumber"`
			BeneficiaryName string `json:"beneficiaryName"`
			SwiftCode       string `json:"swiftCode"`
			PaymentReference string `json:"paymentReference"`
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &swift)
		}
		return &common.PaymentDetails{
			Details: &common.PaymentDetails_Swift_{Swift: &common.PaymentDetails_Swift{
				AccountNumber:    swift.AccountNumber,
				BeneficiaryName:  swift.BeneficiaryName,
				SwiftCode:        swift.SwiftCode,
				PaymentReference: swift.PaymentReference,
			}},
		}
	case "PAYMENT_METHOD_TYPE_FPS", "FPS":
		var fps struct {
			SortCode        string `json:"sortCode"`
			AccountNumber   string `json:"accountNumber"`
			BeneficiaryName string `json:"beneficiaryName"`
			Reference       string `json:"reference"`
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &fps)
		}
		return &common.PaymentDetails{
			Details: &common.PaymentDetails_Fps_{Fps: &common.PaymentDetails_Fps{
				SortCode:        fps.SortCode,
				AccountNumber:   fps.AccountNumber,
				BeneficiaryName: fps.BeneficiaryName,
				Reference:       fps.Reference,
			}},
		}
	default:
		// Fallback: try to unmarshal into a generic structure if the caller
		// supplies a recognized envelope, otherwise return empty details.
		return &common.PaymentDetails{}
	}
}

func buildPaymentReceipt(receipt string) *common.PaymentReceipt {
	return &common.PaymentReceipt{
		Details: &common.PaymentReceipt_Sepa_{Sepa: &common.PaymentReceipt_Sepa{
			BankingTransactionReferenceId: &receipt,
		}},
	}
}

// Person is re-exported from ivms for handler use.
type Person = ivms.Person

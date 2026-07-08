package internal

import (
	"context"
	"log"

	"connectrpc.com/connect"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
)

func GetQuote(ctx context.Context, networkClient paymentconnect.NetworkServiceClient) {
	quote, err := networkClient.GetQuote(ctx, connect.NewRequest(&payment.GetQuoteRequest{
		Amount: &payment.PaymentAmount{Amount: &payment.PaymentAmount_SettlementAmount{
			SettlementAmount: &common.Decimal{Unscaled: 500, Exponent: 0}, // amount in USD
		}},
		PayOutCurrency: "GBP",
		PayOutMethod:   common.PaymentMethodType_PAYMENT_METHOD_TYPE_SWIFT,
		QuoteType:      payment.QuoteType_QUOTE_TYPE_REALTIME,
	}))
	if err != nil {
		log.Printf("Error getting quote: %s\n", err.Error()) // handle errors appropriately
		return
	}

	switch quote.Msg.Result.(type) {
	case *payment.GetQuoteResponse_Success_:
		log.Printf("Got success response with quote id: %d \n", quote.Msg.GetSuccess().QuoteId.QuoteId)
	case *payment.GetQuoteResponse_Failure_:
		log.Printf("Got failure response with reason: %s\n", quote.Msg.GetFailure().Reason.String())
	default:
		log.Println("Unknown type")
	}
}

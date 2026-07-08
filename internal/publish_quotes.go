package internal

import (
	"context"
	"log"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func PublishQuotes(ctx context.Context, networkClient paymentconnect.NetworkServiceClient) {
	// TODO: Step 1.3 replace this with fetching quotes from your systems and publishing them into t-0 Network.
	// We recommend publishing at least once per 5 seconds, but not more than once per second

	const (
		publishInterval = 5 * time.Second
		minInterval     = 1 * time.Second
	)

	ticker := time.NewTicker(publishInterval)
	defer ticker.Stop()

	// keep last-published timestamp so we can enforce minInterval even on retries
	var lastAttempt time.Time

	publish := func() {
		currency := "EUR"
		paymentMethod := common.PaymentMethodType_PAYMENT_METHOD_TYPE_SEPA
		expiration := timestamppb.New(time.Now().Add(30 * time.Second)) // expiration time - 30 seconds from now
		timestamp := timestamppb.New(time.Now())                        // current timestamp

		//NOTE: Every update quote request discard all previous quotes that were published before.
		// So if you want to publish multiple quotes, you need to combine them into a single request.
		// Otherwise, if you send multiple requests, only the quotes from the last one will be available.
		_, err := networkClient.UpdateQuote(ctx, connect.NewRequest(&payment.UpdateQuoteRequest{
			PayOut: []*payment.UpdateQuoteRequest_Quote{ // The quote at which you want to take USDT and pay out local currency (off-ramp)
				{
					Currency:      currency,
					QuoteType:     payment.QuoteType_QUOTE_TYPE_REALTIME, // REALTIME is only supported right now
					PaymentMethod: paymentMethod,
					Expiration:    expiration,
					Timestamp:     timestamp,
					Bands: []*payment.UpdateQuoteRequest_Quote_Band{ // one or more bands are allowed
						{
							ClientQuoteId: uuid.NewString(),
							MaxAmount: &common.Decimal{
								Unscaled: 1000, // maximum amount in USD, could be 1000, 5000, 10000 or 25000
								Exponent: 0,
							},
							// note that rate is always USD/XXX, so that for BRL quote should be USD/BRL
							Rate: &common.Decimal{ //rate 0.86
								Unscaled: 86,
								Exponent: -2,
							},
						},
					},
				},
			},
			PayIn: []*payment.UpdateQuoteRequest_Quote{ // The quote at which you want to take local currency and settle with USDT (on-ramp)
				{
					Currency:      currency,
					QuoteType:     payment.QuoteType_QUOTE_TYPE_REALTIME, // REALTIME is only supported right now
					PaymentMethod: paymentMethod,
					Expiration:    expiration,
					Timestamp:     timestamp,
					Bands: []*payment.UpdateQuoteRequest_Quote_Band{ // one or more bands are allowed
						{
							ClientQuoteId: uuid.NewString(),
							MaxAmount: &common.Decimal{
								Unscaled: 1000, // maximum amount in USD, could be 1000, 5000, 10000 or 25000
								Exponent: 0,
							},
							// note that rate is always USD/XXX, so that for BRL quote should be USD/BRL
							Rate: &common.Decimal{ //rate 0.88
								Unscaled: 88,
								Exponent: -2,
							},
						},
					},
				},
			},
		}))
		if err != nil {
			log.Printf("Error updating quote: %s (will retry next tick)\n", err.Error())
			return
		}
		log.Printf("Published quote: %s/%s off-ramp=0.86 on-ramp=0.88\n", currency, paymentMethod)
	}

	// publish once at startup so we don't wait publishInterval before the first attempt
	publish()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// enforce minInterval between attempts even if a previous publish() ran early
			if elapsed := time.Since(lastAttempt); elapsed < minInterval {
				continue
			}
			lastAttempt = time.Now()
			publish()
		}
	}
}

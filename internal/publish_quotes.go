package internal

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// PublishConfig controls which sides the periodic ticker pushes.
//
// PayOutDefault: if true, ticker pushes a default pay-out quote (mirrors
// the original Phase 1 behavior). If false, ticker leaves pay-out alone —
// only the HTTP /api/v1/quotes/pay-out endpoint (or another process)
// pushes pay-out quotes. This switch exists for the transition period
// where the HTTP endpoint is being rolled out.
//
// PayIn is always pushed.
type PublishConfig struct {
	PayOutDefault bool
}

// PublishQuotesConfigFromEnv reads the PUBLISH_PAY_OUT_DEFAULT env var
// (default true for backward compatibility).
func PublishQuotesConfigFromEnv() PublishConfig {
	v := os.Getenv("PUBLISH_PAY_OUT_DEFAULT")
	if v == "" {
		return PublishConfig{PayOutDefault: true}
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		log.Printf("PUBLISH_PAY_OUT_DEFAULT=%q is not a bool, defaulting to true: %v", v, err)
		return PublishConfig{PayOutDefault: true}
	}
	return PublishConfig{PayOutDefault: b}
}

func PublishQuotes(ctx context.Context, networkClient paymentconnect.NetworkServiceClient, cfg PublishConfig) {
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

		req := &payment.UpdateQuoteRequest{
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
		}

		if cfg.PayOutDefault {
			//NOTE: Every update quote request discard all previous quotes that were published before.
			// So if you want to publish multiple quotes, you need to combine them into a single request.
			// Otherwise, if you send multiple requests, only the quotes from the last one will be available.
			req.PayOut = []*payment.UpdateQuoteRequest_Quote{ // The quote at which you want to take USDT and pay out local currency (off-ramp)
				{
					Currency:      currency,
					QuoteType:     payment.QuoteType_QUOTE_TYPE_REALTIME,
					PaymentMethod: paymentMethod,
					Expiration:    expiration,
					Timestamp:     timestamp,
					Bands: []*payment.UpdateQuoteRequest_Quote_Band{
						{
							ClientQuoteId: uuid.NewString(),
							MaxAmount: &common.Decimal{
								Unscaled: 1000,
								Exponent: 0,
							},
							Rate: &common.Decimal{
								Unscaled: 86,
								Exponent: -2,
							},
						},
					},
				},
			}
		} else {
			log.Println("PublishQuotes: PayOutDefault disabled, ticker only pushes PayIn")
		}

		_, err := networkClient.UpdateQuote(ctx, connect.NewRequest(req))
		if err != nil {
			log.Printf("Error updating quote: %s (will retry next tick)\n", err.Error())
			return
		}
		if cfg.PayOutDefault {
			log.Printf("Published quote: %s/%s off-ramp=0.86 on-ramp=0.88\n", currency, paymentMethod)
		} else {
			log.Printf("Published quote: %s/%s on-ramp=0.88 (PayOut disabled)\n", currency, paymentMethod)
		}
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

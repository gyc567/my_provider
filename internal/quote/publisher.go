package quote

import (
	"context"
	"fmt"
	"log"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Publisher publishes quote snapshots to the t-0 network.
type Publisher struct {
	store   Store
	client  paymentconnect.NetworkServiceClient
	defaultPayOut bool
}

// NewPublisher creates a new quote publisher.
func NewPublisher(store Store, client paymentconnect.NetworkServiceClient, defaultPayOut bool) *Publisher {
	return &Publisher{
		store:         store,
		client:        client,
		defaultPayOut: defaultPayOut,
	}
}

// Publish retrieves the current snapshots from storage and sends them to the network.
func (p *Publisher) Publish(ctx context.Context) error {
	payOut, err := p.loadStream(ctx, StreamTypePayOut, p.defaultPayOut)
	if err != nil {
		return fmt.Errorf("loading pay-out snapshots: %w", err)
	}

	payIn, err := p.loadStream(ctx, StreamTypePayIn, false)
	if err != nil {
		return fmt.Errorf("loading pay-in snapshots: %w", err)
	}

	req := &payment.UpdateQuoteRequest{
		PayOut: toProtoQuotes(payOut),
		PayIn:  toProtoQuotes(payIn),
	}

	_, err = p.client.UpdateQuote(ctx, connect.NewRequest(req))
	if err != nil {
		return fmt.Errorf("UpdateQuote failed: %w", err)
	}

	log.Printf("Published quotes: pay_out=%d groups, pay_in=%d groups", len(req.PayOut), len(req.PayIn))
	return nil
}

func (p *Publisher) loadStream(ctx context.Context, stream StreamType, useDefault bool) ([]QuoteGroup, error) {
	groups, err := p.store.GetSnapshots(ctx, stream)
	if err != nil && err != ErrNotFound {
		return nil, err
	}

	if stream == StreamTypePayOut && useDefault && (err == ErrNotFound || allExpired(groups)) {
		groups = defaultPayOutQuotes()
		if err := p.store.ReplaceSnapshots(ctx, StreamTypePayOut, groups); err != nil {
			return nil, fmt.Errorf("saving default pay-out snapshots: %w", err)
		}
	}

	return filterExpired(groups), nil
}

func allExpired(groups []QuoteGroup) bool {
	now := time.Now().UTC()
	for _, g := range groups {
		if g.Expiration.After(now) {
			return false
		}
	}
	return true
}

func filterExpired(groups []QuoteGroup) []QuoteGroup {
	now := time.Now().UTC()
	out := make([]QuoteGroup, 0, len(groups))
	for _, g := range groups {
		if g.Expiration.After(now) {
			out = append(out, g)
		}
	}
	return out
}

func toProtoQuotes(groups []QuoteGroup) []*payment.UpdateQuoteRequest_Quote {
	out := make([]*payment.UpdateQuoteRequest_Quote, 0, len(groups))
	for _, g := range groups {
		q := &payment.UpdateQuoteRequest_Quote{
			Currency:      g.Currency,
			QuoteType:     payment.QuoteType_QUOTE_TYPE_REALTIME,
			PaymentMethod: common.PaymentMethodType(common.PaymentMethodType_value[g.PaymentMethod]),
			Expiration:    timestamppb.New(g.Expiration),
			Timestamp:     timestamppb.New(g.Timestamp),
			Bands:         make([]*payment.UpdateQuoteRequest_Quote_Band, 0, len(g.Bands)),
		}
		for _, b := range g.Bands {
			band := &payment.UpdateQuoteRequest_Quote_Band{
				ClientQuoteId: b.ClientQuoteID,
				MaxAmount:     &common.Decimal{Unscaled: b.MaxAmount.Unscaled, Exponent: b.MaxAmount.Exponent},
				Rate:          &common.Decimal{Unscaled: b.Rate.Unscaled, Exponent: b.Rate.Exponent},
			}
			q.Bands = append(q.Bands, band)
		}
		out = append(out, q)
	}
	return out
}

// defaultPayOutQuotes returns the original EUR/SEPA default quotes.
func defaultPayOutQuotes() []QuoteGroup {
	now := time.Now().UTC()
	expiration := now.Add(30 * time.Second)
	return []QuoteGroup{
		{
			Currency:      "EUR",
			PaymentMethod: common.PaymentMethodType_PAYMENT_METHOD_TYPE_SEPA.String(),
			Expiration:    expiration,
			Timestamp:     now,
			Bands: []Band{
				{ClientQuoteID: uuid.NewString(), MaxAmount: Decimal{Unscaled: 1000, Exponent: 0}, Rate: Decimal{Unscaled: 86, Exponent: -2}},
			},
		},
	}
}


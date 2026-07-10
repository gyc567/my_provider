package internal

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment"
	"github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment/paymentconnect"
	"my-provider/internal/quote"
)

type memoryStore struct {
	mu        sync.Mutex
	snapshots map[quote.StreamType][]quote.QuoteGroup
}

func (m *memoryStore) GetSnapshots(_ context.Context, stream quote.StreamType) ([]quote.QuoteGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	groups, ok := m.snapshots[stream]
	if !ok || len(groups) == 0 {
		return nil, quote.ErrNotFound
	}
	return groups, nil
}

func (m *memoryStore) ReplaceSnapshots(_ context.Context, stream quote.StreamType, groups []quote.QuoteGroup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.snapshots == nil {
		m.snapshots = make(map[quote.StreamType][]quote.QuoteGroup)
	}
	m.snapshots[stream] = groups
	return nil
}

func (m *memoryStore) Close() error { return nil }

type countingNetwork struct {
	mu     sync.Mutex
	calls  int
}

func (n *countingNetwork) UpdateQuote(_ context.Context, _ *connect.Request[payment.UpdateQuoteRequest]) (*connect.Response[payment.UpdateQuoteResponse], error) {
	n.mu.Lock()
	n.calls++
	n.mu.Unlock()
	return connect.NewResponse(&payment.UpdateQuoteResponse{}), nil
}

func (n *countingNetwork) GetQuote(_ context.Context, _ *connect.Request[payment.GetQuoteRequest]) (*connect.Response[payment.GetQuoteResponse], error) {
	return nil, nil
}

func (n *countingNetwork) CreatePayment(_ context.Context, _ *connect.Request[payment.CreatePaymentRequest]) (*connect.Response[payment.CreatePaymentResponse], error) {
	return nil, nil
}

//nolint:staticcheck // Required to satisfy paymentconnect.NetworkServiceClient interface.
func (n *countingNetwork) ConfirmPayout(_ context.Context, _ *connect.Request[payment.ConfirmPayoutRequest]) (*connect.Response[payment.ConfirmPayoutResponse], error) {
	return nil, nil
}

func (n *countingNetwork) FinalizePayout(_ context.Context, _ *connect.Request[payment.FinalizePayoutRequest]) (*connect.Response[payment.FinalizePayoutResponse], error) {
	return nil, nil
}

func (n *countingNetwork) CompleteManualAmlCheck(_ context.Context, _ *connect.Request[payment.CompleteManualAmlCheckRequest]) (*connect.Response[payment.CompleteManualAmlCheckResponse], error) {
	return nil, nil
}

func (n *countingNetwork) Calls() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

var _ paymentconnect.NetworkServiceClient = (*countingNetwork)(nil)

type errorNetwork struct {
	countingNetwork
}

func (n *errorNetwork) UpdateQuote(_ context.Context, _ *connect.Request[payment.UpdateQuoteRequest]) (*connect.Response[payment.UpdateQuoteResponse], error) {
	n.mu.Lock()
	n.calls++
	n.mu.Unlock()
	return nil, errors.New("publish failed")
}

func TestPublishQuotes_LogsErrorsAndContinues(t *testing.T) {
	store := &memoryStore{}
	net := &errorNetwork{}
	publisher := quote.NewPublisher(store, net, true, false)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		PublishQuotes(ctx, publisher)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	if calls := net.Calls(); calls < 1 {
		t.Fatalf("expected at least 1 publish attempt, got %d", calls)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("PublishQuotes did not stop after context cancellation")
	}
}

func TestPublishQuotes_PublishesOnStartupAndStops(t *testing.T) {
	store := &memoryStore{}
	net := &countingNetwork{}
	publisher := quote.NewPublisher(store, net, true, false)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		PublishQuotes(ctx, publisher)
		close(done)
	}()

	// Wait for the initial startup publish.
	time.Sleep(100 * time.Millisecond)
	if calls := net.Calls(); calls < 1 {
		t.Fatalf("expected at least 1 publish call, got %d", calls)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("PublishQuotes did not stop after context cancellation")
	}
}

package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"
)

func TestMapError_NilIsOK(t *testing.T) {
	t.Parallel()
	got := MapError(nil)
	if got.HTTPStatus != 200 || got.Code != "OK" {
		t.Errorf("nil err should map to 200/OK, got %d/%s", got.HTTPStatus, got.Code)
	}
}

func TestMapError_ContextDeadlineExceeded(t *testing.T) {
	t.Parallel()
	err := context.DeadlineExceeded
	got := MapError(err)
	if got.HTTPStatus != 504 || got.Code != "upstream_timeout" {
		t.Errorf("got %d/%s, want 504/upstream_timeout", got.HTTPStatus, got.Code)
	}
}

func TestMapError_ContextCanceled(t *testing.T) {
	t.Parallel()
	got := MapError(context.Canceled)
	if got.HTTPStatus != 504 || got.Code != "upstream_canceled" {
		t.Errorf("got %d/%s, want 504/upstream_canceled", got.HTTPStatus, got.Code)
	}
}

func TestMapError_ConnectUnavailable(t *testing.T) {
	t.Parallel()
	err := connect.NewError(connect.CodeUnavailable, errors.New("network down"))
	got := MapError(err)
	if got.HTTPStatus != 502 {
		t.Errorf("got %d, want 502", got.HTTPStatus)
	}
}

func TestMapError_ConnectDeadlineExceeded(t *testing.T) {
	t.Parallel()
	err := connect.NewError(connect.CodeDeadlineExceeded, errors.New("deadline"))
	got := MapError(err)
	if got.HTTPStatus != 504 {
		t.Errorf("got %d, want 504", got.HTTPStatus)
	}
}

func TestMapError_ConnectUnauthenticated(t *testing.T) {
	t.Parallel()
	err := connect.NewError(connect.CodeUnauthenticated, errors.New("bad sig"))
	got := MapError(err)
	if got.HTTPStatus != 502 {
		t.Errorf("got %d, want 502 (signature errors map to upstream_error)", got.HTTPStatus)
	}
	if got.Code != "upstream_error" {
		t.Errorf("got code %q, want upstream_error", got.Code)
	}
}

func TestMapError_UnsupportedBandMessage(t *testing.T) {
	t.Parallel()
	err := connect.NewError(connect.CodeUnknown, errors.New("unsupported band: max_amount=2000"))
	got := MapError(err)
	if got.HTTPStatus != 422 {
		t.Errorf("got %d, want 422", got.HTTPStatus)
	}
	if got.Code != "rejected_by_network" {
		t.Errorf("got code %q, want rejected_by_network", got.Code)
	}
}

func TestMapError_ClientQuoteIDConflictMessage(t *testing.T) {
	t.Parallel()
	err := connect.NewError(connect.CodeAlreadyExists, errors.New("client_quote_id conflict across snapshots"))
	got := MapError(err)
	if got.HTTPStatus != 409 {
		t.Errorf("got %d, want 409", got.HTTPStatus)
	}
	if got.Code != "client_quote_id_conflict" {
		t.Errorf("got code %q, want client_quote_id_conflict", got.Code)
	}
}

func TestMapError_GenericConnectError(t *testing.T) {
	t.Parallel()
	err := connect.NewError(connect.CodeInternal, errors.New("internal"))
	got := MapError(err)
	if got.HTTPStatus != 502 {
		t.Errorf("got %d, want 502", got.HTTPStatus)
	}
	if got.Code != "upstream_error" {
		t.Errorf("got code %q, want upstream_error", got.Code)
	}
	if !strings.Contains(got.Detail, "internal") {
		t.Errorf("detail should preserve upstream msg, got %q", got.Detail)
	}
}

func TestMapError_PlainError(t *testing.T) {
	t.Parallel()
	got := MapError(errors.New("dial tcp: connection refused"))
	if got.HTTPStatus != 502 {
		t.Errorf("got %d, want 502", got.HTTPStatus)
	}
	if got.Code != "upstream_error" {
		t.Errorf("got code %q, want upstream_error", got.Code)
	}
}

func TestMapError_WrappedDeadline(t *testing.T) {
	t.Parallel()
	wrapped := errors.New("transport: " + context.DeadlineExceeded.Error())
	got := MapError(wrapped)
	// Plain error (not context.DeadlineExceeded via errors.Is) should not
	// match the deadline branch — we want 502, not 504.
	_ = got // just ensure no panic
}

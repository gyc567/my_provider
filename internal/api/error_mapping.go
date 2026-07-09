package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
)

// APIError is the structured mapping of an upstream or local error to
// the HTTP layer's contract.
type APIError struct {
	HTTPStatus int
	Code       string
	Detail     string
}

// MapError converts an arbitrary error (typically from networkClient.UpdateQuote)
// into an APIError describing the HTTP response. The mapping is conservative:
// prefer 502 (caller should retry later) over 5xx-specific codes unless we
// can prove the error is permanent.
//
// Strategy:
//   - context.DeadlineExceeded / context.Canceled -> 504 (we hit our own timeout)
//   - connect.Error with code DeadlineExceeded        -> 504
//   - connect.Error with code Unauthenticated         -> 502 (we got rejected)
//   - message contains "unsupported band"             -> 422 (network rejected the value)
//   - message contains "client_quote_id" + conflict  -> 409
//   - everything else from connect                    -> 502 + code from upstream
//   - non-connect errors                              -> 502 (transport-level)
func MapError(err error) APIError {
	if err == nil {
		return APIError{HTTPStatus: 200, Code: "OK"}
	}

	// Our own context deadline / cancellation.
	if errors.Is(err, context.DeadlineExceeded) {
		return APIError{HTTPStatus: 504, Code: "upstream_timeout", Detail: err.Error()}
	}
	if errors.Is(err, context.Canceled) {
		return APIError{HTTPStatus: 504, Code: "upstream_canceled", Detail: err.Error()}
	}

	// Connect RPC errors carry a code and a message. We map the code first
	// then sniff the message for sandbox-specific phrasings.
	var connErr *connect.Error
	if errors.As(err, &connErr) {
		msg := connErr.Error()
		if strings.Contains(msg, "unsupported band") {
			return APIError{HTTPStatus: 422, Code: "rejected_by_network", Detail: msg}
		}
		if strings.Contains(msg, "client_quote_id") && (strings.Contains(msg, "conflict") || strings.Contains(msg, "duplicate") || strings.Contains(msg, "already")) {
			return APIError{HTTPStatus: 409, Code: "client_quote_id_conflict", Detail: msg}
		}
		switch connErr.Code() {
		case connect.CodeDeadlineExceeded:
			return APIError{HTTPStatus: 504, Code: "upstream_timeout", Detail: msg}
		case connect.CodeUnauthenticated:
			return APIError{HTTPStatus: 502, Code: "upstream_error",
				Detail: "network rejected our signature: " + msg}
		case connect.CodeUnavailable, connect.CodeUnknown:
			return APIError{HTTPStatus: 502, Code: "upstream_error",
				Detail: fmt.Sprintf("network returned code=%s: %s", connErr.Code(), msg)}
		default:
			return APIError{HTTPStatus: 502, Code: "upstream_error",
				Detail: fmt.Sprintf("network returned code=%s: %s", connErr.Code(), msg)}
		}
	}

	// Plain error (transport-level: dial failures, EOF, etc.).
	return APIError{HTTPStatus: 502, Code: "upstream_error", Detail: err.Error()}
}

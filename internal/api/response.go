package api

import (
	"encoding/json"
	"net/http"
)

// writeError writes a JSON error envelope. It sets Content-Type and
// the given status code, and includes the request_id if non-empty.
func writeError(w http.ResponseWriter, status int, code, detail, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	resp := ErrorResponse{Error: code, Detail: detail, RequestID: requestID}
	_ = json.NewEncoder(w).Encode(resp)
}


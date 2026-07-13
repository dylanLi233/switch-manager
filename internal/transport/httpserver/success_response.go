package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
)

type successEnvelope struct {
	RequestID string `json:"request_id"`
	Success   bool   `json:"success"`
	Data      any    `json:"data"`
}

func WriteSuccess(w http.ResponseWriter, r *http.Request, status int, data any) {
	requestID := ""
	if r != nil {
		requestID = RequestIDFromContext(r.Context())
	}
	if requestID == "" {
		requestID = newRequestID()
	}
	payload, err := json.Marshal(successEnvelope{RequestID: requestID, Success: true, Data: data})
	if err != nil {
		WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(RequestIDHeader, requestID)
	w.WriteHeader(status)
	_, _ = w.Write(append(bytes.TrimSpace(payload), '\n'))
}

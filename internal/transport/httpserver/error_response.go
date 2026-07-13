package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dylanLi233/switch-manager/internal/apperror"
)

// ErrorHandlerFunc is an HTTP handler that returns an application error.
// It must not write a response before returning a non-nil error.
type ErrorHandlerFunc func(http.ResponseWriter, *http.Request) error

// AdaptErrorHandler converts returned errors into the standard JSON envelope.
// The interface return type allows transport middleware to compose wrappers
// without depending on the concrete http.HandlerFunc implementation.
func AdaptErrorHandler(handler ErrorHandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handler == nil {
			WriteError(w, r, errors.New("nil error handler"))
			return
		}
		if err := handler(w, r); err != nil {
			WriteError(w, r, err)
		}
	})
}

type errorEnvelope struct {
	RequestID string    `json:"request_id"`
	Success   bool      `json:"success"`
	Error     errorBody `json:"error"`
}

type errorBody struct {
	Code      apperror.Code `json:"code"`
	Message   string        `json:"message"`
	Retryable bool          `json:"retryable"`
	Details   any           `json:"details,omitempty"`
}

// WriteError writes a safe, machine-readable error response. Wrapped causes are
// retained for trusted logs but are never serialized into the response.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	app := apperror.Normalize(err)
	requestID := ""
	if r != nil {
		requestID = RequestIDFromContext(r.Context())
	}
	if requestID == "" {
		requestID = newRequestID()
	}

	envelope := errorEnvelope{
		RequestID: requestID,
		Success:   false,
		Error: errorBody{
			Code:      app.Code,
			Message:   app.Message,
			Retryable: app.Retryable,
			Details:   app.Details,
		},
	}
	payload, marshalErr := json.Marshal(envelope)
	if marshalErr != nil {
		app = apperror.New(apperror.CodeInternalError, "")
		envelope = errorEnvelope{
			RequestID: requestID,
			Success:   false,
			Error: errorBody{
				Code:      app.Code,
				Message:   app.Message,
				Retryable: app.Retryable,
			},
		}
		payload, _ = json.Marshal(envelope)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set(RequestIDHeader, requestID)
	w.WriteHeader(app.HTTPStatus)
	_, _ = w.Write(append(bytes.TrimSpace(payload), '\n'))
}

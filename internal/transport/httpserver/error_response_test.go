package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/apperror"
)

func TestWriteErrorKnownApplicationError(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = request.WithContext(contextWithRequestID(request.Context(), "req-123"))
	recorder := httptest.NewRecorder()
	internal := errors.New("password=secret")
	WriteError(recorder, request, apperror.Wrap(
		apperror.CodeDeviceUnreachable,
		"switch unavailable",
		internal,
	).WithDetails(map[string]string{"device_id": "sw-1"}))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", recorder.Code)
	}
	if recorder.Header().Get(RequestIDHeader) != "req-123" {
		t.Fatalf("request id=%q", recorder.Header().Get(RequestIDHeader))
	}
	if strings.Contains(recorder.Body.String(), "password") {
		t.Fatal("response leaked cause")
	}
	var body errorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Success || body.Error.Code != apperror.CodeDeviceUnreachable || !body.Error.Retryable {
		t.Fatalf("body=%+v", body)
	}
}

func TestWriteErrorUnknownErrorIsGeneric(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	WriteError(
		recorder,
		httptest.NewRequest(http.MethodGet, "/", nil),
		errors.New("database dsn secret"),
	)
	if recorder.Code != http.StatusInternalServerError ||
		strings.Contains(recorder.Body.String(), "secret") ||
		!strings.Contains(recorder.Body.String(), "INTERNAL_ERROR") {
		t.Fatalf("body=%s", recorder.Body.String())
	}
}

func TestWriteErrorMarshalFailureFallsBack(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	badDetails := map[string]any{"channel": make(chan int)}
	WriteError(
		recorder,
		httptest.NewRequest(http.MethodGet, "/", nil),
		apperror.New(apperror.CodeValidationError, "").WithDetails(badDetails),
	)
	if recorder.Code != http.StatusInternalServerError ||
		!strings.Contains(recorder.Body.String(), "INTERNAL_ERROR") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAdaptErrorHandler(t *testing.T) {
	t.Parallel()
	handler := AdaptErrorHandler(func(http.ResponseWriter, *http.Request) error {
		return apperror.New(apperror.CodeForbidden, "")
	})
	recorder := httptest.NewRecorder()
	withRequestID(handler).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/", nil),
	)
	if recorder.Code != http.StatusForbidden || recorder.Header().Get(RequestIDHeader) == "" {
		t.Fatalf("status=%d id=%q", recorder.Code, recorder.Header().Get(RequestIDHeader))
	}
}

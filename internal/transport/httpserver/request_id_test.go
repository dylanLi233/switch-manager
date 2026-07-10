package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

func contextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDContextKey{}, id)
}

func TestRequestIDMiddlewareAcceptsValidID(t *testing.T) {
	t.Parallel()
	seen := ""
	handler := withRequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, "client:req-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if seen != "client:req-1" || rec.Header().Get(RequestIDHeader) != seen {
		t.Fatalf("seen=%q header=%q", seen, rec.Header().Get(RequestIDHeader))
	}
}

func TestRequestIDMiddlewareReplacesInvalidID(t *testing.T) {
	t.Parallel()
	handler := withRequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, "bad id with spaces")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	got := rec.Header().Get(RequestIDHeader)
	if got == "bad id with spaces" || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(got) {
		t.Fatalf("generated id=%q", got)
	}
}

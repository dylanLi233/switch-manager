package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/health"
)

func TestRouterHealthEndpoints(t *testing.T) {
	t.Parallel()
	h := health.NewHandler(time.Second, health.CheckFunc{
		CheckName: "database",
		Fn:        func(context.Context) error { return errors.New("down") },
	})
	router := NewRouter(h, 1024)

	live := httptest.NewRecorder()
	router.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if live.Code != http.StatusOK {
		t.Fatalf("live status = %d", live.Code)
	}
	if live.Header().Get(RequestIDHeader) == "" {
		t.Fatal("liveness response is missing a request ID")
	}

	ready := httptest.NewRecorder()
	router.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d", ready.Code)
	}
	if ready.Header().Get(RequestIDHeader) == "" {
		t.Fatal("readiness response is missing a request ID")
	}
}

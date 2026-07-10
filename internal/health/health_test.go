package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLive(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	NewHandler(time.Second).Live(rr, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestReadyFailsWhenDependencyFails(t *testing.T) {
	t.Parallel()
	h := NewHandler(time.Second, CheckFunc{
		CheckName: "database",
		Fn:        func(context.Context) error { return errors.New("unavailable") },
	})
	rr := httptest.NewRecorder()
	h.Ready(rr, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestReadySucceedsWhenChecksPass(t *testing.T) {
	t.Parallel()
	h := NewHandler(time.Second, CheckFunc{
		CheckName: "database",
		Fn:        func(context.Context) error { return nil },
	})
	rr := httptest.NewRecorder()
	h.Ready(rr, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
}

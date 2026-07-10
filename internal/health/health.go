// Package health implements liveness and readiness checks.
package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// Check reports whether a dependency is ready.
type Check interface {
	Name() string
	Check(context.Context) error
}

// CheckFunc adapts a function into a named health check.
type CheckFunc struct {
	CheckName string
	Fn        func(context.Context) error
}

// Name returns the stable check name.
func (c CheckFunc) Name() string { return c.CheckName }

// Check executes the underlying function.
func (c CheckFunc) Check(ctx context.Context) error {
	if c.Fn == nil {
		return errors.New("health check function is nil")
	}
	return c.Fn(ctx)
}

// Handler serves liveness and readiness endpoints.
type Handler struct {
	checks  []Check
	timeout time.Duration
}

// NewHandler creates a health handler.
func NewHandler(timeout time.Duration, checks ...Check) *Handler {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Handler{checks: checks, timeout: timeout}
}

// Live reports whether the process can serve HTTP.
func (h *Handler) Live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "alive"})
}

// Ready reports whether configured dependencies are available.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	failures := make(map[string]string)
	for _, check := range h.checks {
		if check == nil {
			failures["unknown"] = "nil health check"
			continue
		}
		if err := check.Check(ctx); err != nil {
			failures[check.Name()] = err.Error()
		}
	}
	if len(failures) > 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "not_ready",
			"checks": failures,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

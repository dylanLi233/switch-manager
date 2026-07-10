// Package httpserver contains the HTTP transport bootstrap.
package httpserver

import (
	"net/http"

	"github.com/dylanLi233/switch-manager/internal/health"
)

// NewRouter builds the TASK-001 HTTP routes.
func NewRouter(healthHandler *health.Handler, maxRequestBytes int64) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", healthHandler.Live)
	mux.HandleFunc("GET /health/ready", healthHandler.Ready)

	return limitRequestBody(maxRequestBytes, mux)
}

func limitRequestBody(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if maxBytes > 0 && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

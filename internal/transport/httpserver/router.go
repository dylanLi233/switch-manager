// Package httpserver contains the HTTP transport bootstrap.
package httpserver

import (
	"net/http"

	"github.com/dylanLi233/switch-manager/internal/health"
)

// NewRouter builds public health routes without authentication.
func NewRouter(healthHandler *health.Handler, maxRequestBytes int64) http.Handler {
	return newRouter(healthHandler, maxRequestBytes, nil)
}

// NewAuthenticatedRouter builds health routes plus the protected auth integration endpoint.
func NewAuthenticatedRouter(healthHandler *health.Handler, maxRequestBytes int64, authenticator Authenticator) http.Handler {
	return newRouter(healthHandler, maxRequestBytes, authenticator)
}

func newRouter(healthHandler *health.Handler, maxRequestBytes int64, authenticator Authenticator) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", healthHandler.Live)
	mux.HandleFunc("GET /health/ready", healthHandler.Ready)
	if authenticator != nil {
		mux.Handle("GET /api/v1/auth/me", AuthenticationMiddleware(authenticator)(http.HandlerFunc(AuthMeHandler)))
	}
	return withRequestID(limitRequestBody(maxRequestBytes, mux))
}

func limitRequestBody(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if maxBytes > 0 && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

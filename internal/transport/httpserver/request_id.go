package httpserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"sync/atomic"
	"time"
)

// RequestIDHeader is the request correlation header accepted and returned by the API.
const RequestIDHeader = "X-Request-ID"

type requestIDContextKey struct{}

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var fallbackRequestIDCounter atomic.Uint64

// RequestIDFromContext returns the validated request ID installed by the router.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(requestIDContextKey{}).(string)
	return value
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !validRequestID.MatchString(id) {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDContextKey{}, id)))
	})
}

func newRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	seed := fmt.Sprintf("%d:%d", time.Now().UnixNano(), fallbackRequestIDCounter.Add(1))
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:16])
}

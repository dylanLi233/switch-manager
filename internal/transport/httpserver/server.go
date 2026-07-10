package httpserver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// Server wraps http.Server with context-driven graceful shutdown.
type Server struct {
	server          *http.Server
	shutdownTimeout time.Duration
}

// New creates an HTTP server.
func New(
	handler http.Handler,
	readTimeout time.Duration,
	writeTimeout time.Duration,
	shutdownTimeout time.Duration,
) *Server {
	return &Server{
		server: &http.Server{
			Handler:           handler,
			ReadTimeout:       readTimeout,
			ReadHeaderTimeout: readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       60 * time.Second,
		},
		shutdownTimeout: shutdownTimeout,
	}
}

// Serve blocks until the listener fails or the context is cancelled.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	serveErr := make(chan error, 1)
	go func() {
		err := s.server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()
		if err := s.server.Shutdown(shutdownCtx); err != nil {
			_ = s.server.Close()
			return err
		}
		return <-serveErr
	}
}

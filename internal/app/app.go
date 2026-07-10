// Package app wires process dependencies.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/dylanLi233/switch-manager/internal/authn"
	"github.com/dylanLi233/switch-manager/internal/config"
	"github.com/dylanLi233/switch-manager/internal/health"
	"github.com/dylanLi233/switch-manager/internal/infrastructure/postgres"
	"github.com/dylanLi233/switch-manager/internal/transport/httpserver"
)

// App owns process-level dependencies and server lifecycle.
type App struct {
	cfg    config.Config
	logger *slog.Logger
	server *httpserver.Server
	store  *postgres.Store
}

// New validates configuration and wires optional database-backed authentication.
func New(ctx context.Context, cfg config.Config, logger *slog.Logger) (*App, error) {
	if ctx == nil {
		return nil, errors.New("bootstrap context is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	if logger == nil {
		return nil, errors.New("logger is required")
	}

	app := &App{cfg: cfg, logger: logger}
	checks := []health.Check{}
	needsDatabase := cfg.Database.Required || cfg.Authentication.Enabled
	if needsDatabase {
		store, err := postgres.Open(ctx, cfg.Database.DSN)
		if err != nil {
			return nil, fmt.Errorf("open PostgreSQL: %w", err)
		}
		app.store = store
		checks = append(checks, health.CheckFunc{CheckName: "database", Fn: store.Ping})
	}

	healthHandler := health.NewHandler(cfg.Server.ReadTimeout, checks...)
	var router http.Handler
	if cfg.Authentication.Enabled {
		verifier, err := authn.NewJWTVerifierFromFile(cfg.Authentication.PublicKeyFile, authn.JWTConfig{
			Issuer: cfg.Authentication.Issuer, Audience: cfg.Authentication.Audience,
			KeyID: cfg.Authentication.KeyID, ClockSkew: cfg.Authentication.ClockSkew,
			UsernameClaim: cfg.Authentication.UsernameClaim,
			ServiceActorClaim: cfg.Authentication.ServiceActorClaim,
		})
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize JWT verifier: %w", err)
		}
		authentication, err := authn.NewService(verifier, app.store.Repositories().Access)
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize authentication service: %w", err)
		}
		router = httpserver.NewAuthenticatedRouter(healthHandler, cfg.Server.MaxRequestBytes, authentication)
	} else {
		router = httpserver.NewRouter(healthHandler, cfg.Server.MaxRequestBytes)
	}

	app.server = httpserver.New(
		router,
		cfg.Server.ReadTimeout,
		cfg.Server.WriteTimeout,
		cfg.Server.ShutdownTimeout,
	)
	return app, nil
}

// Close releases process-level dependencies. It is safe to call repeatedly.
func (a *App) Close() {
	if a != nil && a.store != nil {
		a.store.Close()
		a.store = nil
	}
}

// Run listens and serves until context cancellation.
func (a *App) Run(ctx context.Context) error {
	defer a.Close()
	listener, err := net.Listen("tcp", a.cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", a.cfg.Server.Listen, err)
	}
	a.logger.Info("http server started", "address", listener.Addr().String())
	if err := a.server.Serve(ctx, listener); err != nil {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	a.logger.Info("http server stopped")
	return nil
}

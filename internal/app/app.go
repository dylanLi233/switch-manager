// Package app wires process dependencies.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/dylanLi233/switch-manager/internal/config"
	"github.com/dylanLi233/switch-manager/internal/health"
	"github.com/dylanLi233/switch-manager/internal/transport/httpserver"
)

// App owns the process-level server lifecycle.
type App struct {
	cfg    config.Config
	logger *slog.Logger
	server *httpserver.Server
}

// New validates and wires the application.
func New(cfg config.Config, logger *slog.Logger) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	if logger == nil {
		return nil, errors.New("logger is required")
	}

	checks := []health.Check{}
	if cfg.Database.Required {
		checks = append(checks, health.CheckFunc{
			CheckName: "database_configuration",
			Fn: func(context.Context) error {
				if cfg.Database.DSN == "" {
					return errors.New("database DSN is not configured")
				}
				return nil
			},
		})
	}
	healthHandler := health.NewHandler(cfg.Server.ReadTimeout, checks...)
	router := httpserver.NewRouter(healthHandler, cfg.Server.MaxRequestBytes)

	return &App{
		cfg:    cfg,
		logger: logger,
		server: httpserver.New(
			router,
			cfg.Server.ReadTimeout,
			cfg.Server.WriteTimeout,
			cfg.Server.ShutdownTimeout,
		),
	}, nil
}

// Run listens and serves until context cancellation.
func (a *App) Run(ctx context.Context) error {
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

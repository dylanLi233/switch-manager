// Package app wires process dependencies.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"

	"github.com/dylanLi233/switch-manager/internal/authn"
	"github.com/dylanLi233/switch-manager/internal/backupfs"
	"github.com/dylanLi233/switch-manager/internal/concurrency"
	"github.com/dylanLi233/switch-manager/internal/config"
	"github.com/dylanLi233/switch-manager/internal/fakeruntime"
	"github.com/dylanLi233/switch-manager/internal/health"
	"github.com/dylanLi233/switch-manager/internal/infrastructure/postgres"
	"github.com/dylanLi233/switch-manager/internal/inventorysvc"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
	"github.com/dylanLi233/switch-manager/internal/pluginregistry"
	"github.com/dylanLi233/switch-manager/internal/scheduler"
	"github.com/dylanLi233/switch-manager/internal/secretbox"
	"github.com/dylanLi233/switch-manager/internal/sshprobe"
	"github.com/dylanLi233/switch-manager/internal/transport/httpserver"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
	fakeplugin "github.com/dylanLi233/switch-manager/plugins/fake"
	"golang.org/x/crypto/ssh/knownhosts"
)

type App struct {
	cfg           config.Config
	logger        *slog.Logger
	server        *httpserver.Server
	store         *postgres.Store
	dispatcher    *scheduler.Scheduler
	backupStorage *backupfs.Storage
}

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
	inventoryConfig, err := config.LoadInventoryEnvironment(os.LookupEnv)
	if err != nil {
		return nil, fmt.Errorf("load inventory security configuration: %w", err)
	}
	fakeConfig, err := config.LoadFakeRuntimeEnvironment(os.LookupEnv)
	if err != nil {
		return nil, fmt.Errorf("load fake runtime configuration: %w", err)
	}
	queryLimits, err := config.LoadQueryLimits(os.LookupEnv)
	if err != nil {
		return nil, fmt.Errorf("load query limits: %w", err)
	}
	backupConfig, err := config.LoadBackupEnvironment(os.LookupEnv)
	if err != nil {
		return nil, fmt.Errorf("load backup storage configuration: %w", err)
	}
	if inventoryConfig.Enabled && !cfg.Authentication.Enabled {
		return nil, errors.New("inventory API requires authentication to be enabled")
	}
	if fakeConfig.Enabled && !inventoryConfig.Enabled {
		return nil, errors.New("fake plugin runtime requires the inventory API and credential master key")
	}

	app := &App{cfg: cfg, logger: logger}
	checks := []health.Check{}
	if backupConfig.Enabled {
		storage, err := backupfs.New(backupfs.Config{RootDir: backupConfig.RootDir, MaxFileBytes: backupConfig.MaxFileBytes})
		if err != nil {
			return nil, fmt.Errorf("initialize backup storage: %w", err)
		}
		if err := storage.CheckReady(ctx); err != nil {
			return nil, fmt.Errorf("verify backup storage: %w", err)
		}
		app.backupStorage = storage
		checks = append(checks, health.CheckFunc{CheckName: "backup_storage", Fn: storage.CheckReady})
	}
	needsDatabase := cfg.Database.Required || cfg.Authentication.Enabled || inventoryConfig.Enabled || fakeConfig.Enabled
	if needsDatabase {
		store, err := postgres.Open(ctx, cfg.Database.DSN)
		if err != nil {
			return nil, fmt.Errorf("open PostgreSQL: %w", err)
		}
		app.store = store
		checks = append(checks, health.CheckFunc{CheckName: "database", Fn: store.Ping})
	}

	var authentication *authn.Service
	var registrars []httpserver.ProtectedRouteRegistrar
	if cfg.Authentication.Enabled {
		accessRepository := app.store.Repositories().Access
		if err := accessRepository.CheckReady(ctx); err != nil {
			app.Close()
			return nil, fmt.Errorf("verify RBAC schema: %w", err)
		}
		checks = append(checks, health.CheckFunc{CheckName: "authorization_schema", Fn: accessRepository.CheckReady})
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
		authentication, err = authn.NewService(verifier, accessRepository)
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize authentication service: %w", err)
		}
	}

	var fakeFactory *fakeruntime.Factory
	if fakeConfig.Enabled {
		fakeFactory = fakeruntime.New()
	}

	if inventoryConfig.Enabled {
		box, err := secretbox.NewBase64(inventoryConfig.MasterKeyBase64, inventoryConfig.KeyVersion)
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize credential encryption: %w", err)
		}
		var tester inventorysvc.ConnectionTester
		if inventoryConfig.KnownHostsFile != "" {
			callback, err := knownhosts.New(inventoryConfig.KnownHostsFile)
			if err != nil {
				app.Close()
				return nil, fmt.Errorf("load SSH known_hosts: %w", err)
			}
			var prompt sshprobe.PromptProber
			if inventoryConfig.PromptPattern != "" {
				pattern, err := regexp.Compile(inventoryConfig.PromptPattern)
				if err != nil {
					app.Close()
					return nil, fmt.Errorf("compile SSH prompt pattern: %w", err)
				}
				prompt = sshprobe.RegexPromptProber{Pattern: pattern, Family: inventoryConfig.PromptFamily, Timeout: inventoryConfig.SSHTimeout}
			}
			tester, err = sshprobe.New(callback, inventoryConfig.SSHTimeout, prompt)
			if err != nil {
				app.Close()
				return nil, fmt.Errorf("initialize SSH connection tester: %w", err)
			}
		}
		repositories := app.store.Repositories()
		var detector inventorysvc.IdentityDetector
		if fakeFactory != nil {
			detector = fakeFactory
		}
		inventoryService, err := inventorysvc.New(repositories.Devices, repositories.Credentials, repositories.ExecutionCredentials, box, tester, detector)
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize inventory service: %w", err)
		}
		handlers, err := httpserver.NewInventoryHandlers(inventoryService)
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize inventory handlers: %w", err)
		}
		registrars = append(registrars, handlers)
	}

	if fakeFactory != nil {
		repositories := app.store.Repositories()
		registry := pluginregistry.NewCurrent()
		for _, vendor := range []pluginapi.Vendor{pluginapi.VendorHuawei, pluginapi.VendorH3C} {
			plugin, err := fakeplugin.New(vendor)
			if err != nil {
				app.Close()
				return nil, fmt.Errorf("initialize fake plugin %s: %w", vendor, err)
			}
			if err := registry.Register(plugin); err != nil {
				app.Close()
				return nil, fmt.Errorf("register fake plugin %s: %w", vendor, err)
			}
		}
		planner, err := operationsvc.NewPlanner(repositories.Devices, registry)
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize operation planner: %w", err)
		}
		batchStore, err := postgres.NewBatchStore(app.store)
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize batch persistence: %w", err)
		}
		batchAwareTasks, err := operationsvc.NewBatchAwareTaskRepository(repositories.Tasks, batchStore)
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize batch-aware task repository: %w", err)
		}
		guards, err := concurrency.NewController(concurrency.DefaultGlobalLimit)
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize operation guards: %w", err)
		}
		executor, err := operationsvc.NewExecutor(planner, repositories.Audits, fakeFactory, guards)
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize operation executor: %w", err)
		}
		dispatcher, err := scheduler.New(batchAwareTasks, executor, scheduler.Config{Workers: fakeConfig.Workers})
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize task scheduler: %w", err)
		}
		committer, err := postgres.NewOperationSubmission(app.store)
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize operation submission: %w", err)
		}
		operationService, err := operationsvc.NewService(batchAwareTasks, repositories.Audits, planner, dispatcher, committer, operationsvc.Config{SyncWaitTimeout: fakeConfig.SyncWaitTimeout})
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize operation service: %w", err)
		}
		batchService, err := operationsvc.NewBatchService(batchStore, planner, dispatcher, operationsvc.Config{SyncWaitTimeout: fakeConfig.SyncWaitTimeout})
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize batch service: %w", err)
		}
		vlanHandlers, err := httpserver.NewVLANHandlers(operationService)
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize VLAN handlers: %w", err)
		}
		interfaceHandlers, err := httpserver.NewInterfaceHandlers(operationService)
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize interface handlers: %w", err)
		}
		routeACLHandlers, err := httpserver.NewRouteACLHandlers(operationService)
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize route and ACL handlers: %w", err)
		}
		telemetryHandlers, err := httpserver.NewTelemetryHandlers(operationService, queryLimits.ResultLimit)
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize telemetry handlers: %w", err)
		}
		batchHandlers, err := httpserver.NewBatchHandlers(batchService)
		if err != nil {
			app.Close()
			return nil, fmt.Errorf("initialize batch handlers: %w", err)
		}
		registrars = append(registrars, vlanHandlers, interfaceHandlers, routeACLHandlers, telemetryHandlers, batchHandlers)
		app.dispatcher = dispatcher
	}

	healthHandler := health.NewHandler(cfg.Server.ReadTimeout, checks...)
	var router http.Handler
	if authentication != nil {
		router = httpserver.NewAuthenticatedRouter(healthHandler, cfg.Server.MaxRequestBytes, authentication, registrars...)
	} else {
		router = httpserver.NewRouter(healthHandler, cfg.Server.MaxRequestBytes)
	}
	app.server = httpserver.New(router, cfg.Server.ReadTimeout, cfg.Server.WriteTimeout, cfg.Server.ShutdownTimeout)
	return app, nil
}

func (a *App) Close() {
	if a != nil && a.store != nil {
		a.store.Close()
		a.store = nil
	}
}

func (a *App) Run(ctx context.Context) error {
	defer a.Close()
	listener, err := net.Listen("tcp", a.cfg.Server.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", a.cfg.Server.Listen, err)
	}
	a.logger.Info("http server started", "address", listener.Addr().String())
	if a.dispatcher == nil {
		if err := a.server.Serve(ctx, listener); err != nil {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		a.logger.Info("http server stopped")
		return nil
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type componentResult struct {
		name string
		err  error
	}
	results := make(chan componentResult, 2)
	go func() { results <- componentResult{name: "scheduler", err: a.dispatcher.Run(runCtx)} }()
	go func() { results <- componentResult{name: "http", err: a.server.Serve(runCtx, listener)} }()
	var firstErr error
	for completed := 0; completed < 2; completed++ {
		result := <-results
		if result.err != nil && !errors.Is(result.err, context.Canceled) && firstErr == nil {
			firstErr = fmt.Errorf("%s component: %w", result.name, result.err)
			cancel()
		}
		if completed == 0 && ctx.Err() == nil {
			cancel()
		}
	}
	a.logger.Info("http server and task scheduler stopped")
	return firstErr
}

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/dylanLi233/switch-manager/internal/app"
	"github.com/dylanLi233/switch-manager/internal/config"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("switch-manager", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "path to YAML configuration file")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	cfg, err := config.Load(*configPath, os.LookupEnv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "configuration error: %v\n", err)
		return 1
	}
	logger, err := newLogger(cfg, stdout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "logger error: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	application, err := app.New(ctx, cfg, logger)
	if err != nil {
		logger.Error("application bootstrap failed", "error", err)
		return 1
	}
	defer application.Close()

	if err := application.Run(ctx); err != nil {
		logger.Error("application stopped with error", "error", err)
		return 1
	}
	return 0
}

func newLogger(cfg config.Config, output io.Writer) (*slog.Logger, error) {
	var level slog.Level
	switch strings.ToLower(cfg.Logging.Level) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, fmt.Errorf("unsupported log level %q", cfg.Logging.Level)
	}

	options := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch strings.ToLower(cfg.Logging.Format) {
	case "json":
		handler = slog.NewJSONHandler(output, options)
	case "text":
		handler = slog.NewTextHandler(output, options)
	default:
		return nil, fmt.Errorf("unsupported log format %q", cfg.Logging.Format)
	}
	return slog.New(handler), nil
}

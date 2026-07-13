package app

import (
	"context"
	"log/slog"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/config"
)

func TestNewRejectsNilLogger(t *testing.T) {
	if _, err := New(context.Background(), config.Default(), nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewRejectsNilContext(t *testing.T) {
	if _, err := New(nil, config.Default(), slog.Default()); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewAcceptsValidConfig(t *testing.T) {
	application, err := New(context.Background(), config.Default(), slog.Default())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	application.Close()
}

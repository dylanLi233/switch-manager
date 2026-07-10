package app

import (
	"log/slog"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/config"
)

func TestNewRejectsNilLogger(t *testing.T) {
	t.Parallel()
	if _, err := New(config.Default(), nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewAcceptsValidConfig(t *testing.T) {
	t.Parallel()
	if _, err := New(config.Default(), slog.Default()); err != nil {
		t.Fatalf("New() error = %v", err)
	}
}

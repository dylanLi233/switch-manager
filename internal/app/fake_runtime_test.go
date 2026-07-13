package app

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/config"
)

func TestFakeRuntimeRequiresInventorySecurity(t *testing.T) {
	t.Setenv(config.FakeRuntimeEnabledEnv, "true")
	_, err := New(context.Background(), config.Default(), slog.Default())
	if err == nil || !strings.Contains(err.Error(), "credential master key") {
		t.Fatalf("error=%v", err)
	}
}

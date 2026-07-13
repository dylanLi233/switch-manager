package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/config"
)

func TestInventoryAPIRequiresAuthentication(t *testing.T) {
	t.Setenv(config.InventoryMasterKeyEnv, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	_, err := New(context.Background(), config.Default(), slog.Default())
	if err == nil || !strings.Contains(err.Error(), "requires authentication") {
		t.Fatalf("error=%v", err)
	}
}

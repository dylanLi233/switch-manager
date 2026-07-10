package postgres

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
)

func TestTaskRequestImmutabilityIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is not set")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	runMigration(t, root, dsn, "down", "all")
	runMigration(t, root, dsn, "up")

	ctx := context.Background()
	store, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	userID := "00000000-0000-0000-0000-000000000301"
	if _, err := store.pool.Exec(ctx, `INSERT INTO users(id, external_subject, username) VALUES ($1::uuid,$2,$3)`, userID, "task-immutable", "task-immutable"); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	repo := store.Repositories().Tasks
	created, err := repo.Create(ctx, task.Persisted{
		Task: task.Task{
			ID: "00000000-0000-0000-0000-000000000302",
			Type: task.TypeOperation,
			Operation: operation.Name("vlan.create"),
			TargetType: "switch",
			TargetID: "sw-original",
			Status: task.StatusPending,
			ExecutionMode: operation.ExecutionModeSync,
			Payload: json.RawMessage(`{"vlan_id":100}`),
			CreatedBy: userID,
			CreatedAt: now,
			Version: 1,
		},
		IdempotencyKey: "immutable-idem",
	})
	if err != nil {
		t.Fatalf("Create task: %v", err)
	}

	mutated := created
	mutated.TargetID = "sw-mutated"
	mutated.Status = task.StatusQueued
	mutated.Version = 2
	if _, err := repo.Save(ctx, mutated, 1); !apperror.IsCode(err, apperror.CodeStateConflict) {
		t.Fatalf("immutable task save error = %v", err)
	}

	unchanged, err := repo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get task: %v", err)
	}
	if unchanged.TargetID != "sw-original" || unchanged.Status != task.StatusPending || unchanged.Version != 1 {
		t.Fatalf("task changed after rejected mutation: %+v", unchanged)
	}
}

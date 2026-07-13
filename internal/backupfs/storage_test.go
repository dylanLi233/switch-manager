package backupfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dylanLi233/switch-manager/internal/apperror"
)

func newTestStorage(t *testing.T, max int64) (*Storage, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "backups")
	storage, err := New(Config{RootDir: root, MaxFileBytes: max})
	if err != nil {
		t.Fatal(err)
	}
	return storage, root
}

func TestPutVerifyAndPermissions(t *testing.T) {
	storage, root := newTestStorage(t, 1024)
	content := []byte("display current-configuration\nfixture")
	artifact, err := storage.Put(context.Background(), "device-1/backup-1.cfg", bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	if artifact.RelativePath != "device-1/backup-1.cfg" || artifact.SizeBytes != int64(len(content)) || len(artifact.SHA256) != 64 {
		t.Fatalf("artifact=%+v", artifact)
	}
	if filepath.IsAbs(artifact.RelativePath) {
		t.Fatalf("relative path became absolute: %q", artifact.RelativePath)
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if rootInfo.Mode().Perm() != 0o700 {
		t.Fatalf("root mode=%#o", rootInfo.Mode().Perm())
	}
	fileInfo, err := os.Stat(filepath.Join(root, filepath.FromSlash(artifact.RelativePath)))
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("file mode=%#o", fileInfo.Mode().Perm())
	}
	file, err := storage.OpenVerified(context.Background(), artifact.RelativePath, artifact.SHA256, artifact.SizeBytes)
	if err != nil {
		t.Fatal(err)
	}
	read, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil || !bytes.Equal(read, content) {
		t.Fatalf("read=%q err=%v", read, err)
	}
}

func TestTamperingIsRejected(t *testing.T) {
	storage, root := newTestStorage(t, 1024)
	artifact, err := storage.Put(context.Background(), "device-1/backup.cfg", strings.NewReader("original"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "device-1", "backup.cfg"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := storage.Verify(context.Background(), artifact.RelativePath, artifact.SHA256, artifact.SizeBytes); !apperror.IsCode(err, apperror.CodeBackupIntegrityFailed) {
		t.Fatalf("error=%v", err)
	}
}

func TestPathTraversalAndAbsolutePathsAreRejected(t *testing.T) {
	storage, root := newTestStorage(t, 1024)
	outside := filepath.Join(filepath.Dir(root), "outside.cfg")
	for _, path := range []string{"../outside.cfg", "device/../../outside.cfg", "/tmp/outside.cfg", `device\\outside.cfg`, "device//backup.cfg", "device/./backup.cfg", " device/backup.cfg", "device/backup.cfg\n"} {
		if _, err := storage.Put(context.Background(), path, strings.NewReader("data")); !apperror.IsCode(err, apperror.CodeValidationError) {
			t.Errorf("path=%q error=%v", path, err)
		}
	}
	if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside file exists or stat failed unexpectedly: %v", err)
	}
}

func TestSymlinkTraversalIsRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	storage, root := newTestStorage(t, 1024)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Put(context.Background(), "escape/backup.cfg", strings.NewReader("data")); !apperror.IsCode(err, apperror.CodeBackupIntegrityFailed) {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "backup.cfg")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside backup exists or stat failed unexpectedly: %v", err)
	}
}

func TestImmutablePathCannotBeOverwritten(t *testing.T) {
	storage, _ := newTestStorage(t, 1024)
	if _, err := storage.Put(context.Background(), "device/backup.cfg", strings.NewReader("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.Put(context.Background(), "device/backup.cfg", strings.NewReader("second")); !apperror.IsCode(err, apperror.CodeStateConflict) {
		t.Fatalf("error=%v", err)
	}
}

func TestSizeLimitRemovesTemporaryFile(t *testing.T) {
	storage, root := newTestStorage(t, 4)
	if _, err := storage.Put(context.Background(), "device/large.cfg", strings.NewReader("12345")); !apperror.IsCode(err, apperror.CodeResultTooLarge) {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "device", "large.cfg")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("large backup exists: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "device"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".backup-tmp-") {
			t.Fatalf("temporary file leaked: %s", entry.Name())
		}
	}
}

func TestCancelledWriteDoesNotCommit(t *testing.T) {
	storage, root := newTestStorage(t, 1024)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := storage.Put(ctx, "device/cancelled.cfg", strings.NewReader("data")); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "device", "cancelled.cfg")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled backup exists: %v", err)
	}
}

func TestDeleteAndMissingBackup(t *testing.T) {
	storage, _ := newTestStorage(t, 1024)
	artifact, err := storage.Put(context.Background(), "device/backup.cfg", strings.NewReader("data"))
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Delete(context.Background(), artifact.RelativePath); err != nil {
		t.Fatal(err)
	}
	if err := storage.Verify(context.Background(), artifact.RelativePath, artifact.SHA256, artifact.SizeBytes); !apperror.IsCode(err, apperror.CodeBackupNotFound) {
		t.Fatalf("error=%v", err)
	}
}

func TestCheckReadyRejectsUnsafePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions are not available")
	}
	storage, root := newTestStorage(t, 1024)
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := storage.CheckReady(context.Background()); err == nil {
		t.Fatal("expected unsafe permissions error")
	}
}

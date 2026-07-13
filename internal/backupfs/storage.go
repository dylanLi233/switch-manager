// Package backupfs stores immutable configuration backups on a local filesystem.
package backupfs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/backup"
)

const (
	defaultMaxFileBytes int64       = 10 << 20
	rootDirectoryMode   os.FileMode = 0o700
	backupFileMode      os.FileMode = 0o600
)

// Config controls local immutable backup storage.
type Config struct {
	RootDir      string
	MaxFileBytes int64
}

// Storage writes immutable files below one resolved root directory.
type Storage struct {
	root         string
	maxFileBytes int64
}

func New(config Config) (*Storage, error) {
	root := strings.TrimSpace(config.RootDir)
	if root == "" {
		return nil, errors.New("backup root directory is required")
	}
	if config.MaxFileBytes == 0 {
		config.MaxFileBytes = defaultMaxFileBytes
	}
	if config.MaxFileBytes < 1 {
		return nil, errors.New("backup max file bytes must be positive")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve backup root: %w", err)
	}
	if err := os.MkdirAll(absolute, rootDirectoryMode); err != nil {
		return nil, fmt.Errorf("create backup root: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("inspect backup root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("backup root must be a real directory")
	}
	if err := os.Chmod(absolute, rootDirectoryMode); err != nil {
		return nil, fmt.Errorf("secure backup root permissions: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve backup root symlinks: %w", err)
	}
	return &Storage{root: resolved, maxFileBytes: config.MaxFileBytes}, nil
}

func (s *Storage) Put(ctx context.Context, relativePath string, source io.Reader) (backup.Artifact, error) {
	if ctx == nil {
		return backup.Artifact{}, errors.New("context is required")
	}
	if source == nil {
		return backup.Artifact{}, apperror.New(apperror.CodeValidationError, "backup source is required")
	}
	clean, target, err := s.resolveForWrite(relativePath)
	if err != nil {
		return backup.Artifact{}, err
	}
	if err := ensureSecureDirectory(filepath.Dir(target), s.root); err != nil {
		return backup.Artifact{}, err
	}
	if _, err := os.Lstat(target); err == nil {
		return backup.Artifact{}, apperror.New(apperror.CodeStateConflict, "backup path already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return backup.Artifact{}, apperror.Wrap(apperror.CodeBackupFailed, "", err)
	}

	temp, err := os.OpenFile(filepath.Join(filepath.Dir(target), temporaryName()), os.O_CREATE|os.O_EXCL|os.O_WRONLY, backupFileMode)
	if err != nil {
		return backup.Artifact{}, apperror.Wrap(apperror.CodeBackupFailed, "", err)
	}
	tempName := temp.Name()
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempName)
		}
	}()

	hash := sha256.New()
	written, err := copyWithContext(ctx, io.MultiWriter(temp, hash), source, s.maxFileBytes)
	if err != nil {
		return backup.Artifact{}, err
	}
	if err := temp.Sync(); err != nil {
		return backup.Artifact{}, apperror.Wrap(apperror.CodeBackupFailed, "", err)
	}
	if err := temp.Chmod(backupFileMode); err != nil {
		return backup.Artifact{}, apperror.Wrap(apperror.CodeBackupFailed, "", err)
	}
	if err := temp.Close(); err != nil {
		return backup.Artifact{}, apperror.Wrap(apperror.CodeBackupFailed, "", err)
	}
	// A hard-link commit is atomic and refuses to replace an existing immutable backup.
	if err := os.Link(tempName, target); err != nil {
		if errors.Is(err, os.ErrExist) {
			return backup.Artifact{}, apperror.New(apperror.CodeStateConflict, "backup path already exists")
		}
		return backup.Artifact{}, apperror.Wrap(apperror.CodeBackupFailed, "", err)
	}
	committed = true
	_ = os.Remove(tempName)
	if err := syncDirectory(filepath.Dir(target)); err != nil {
		_ = os.Remove(target)
		return backup.Artifact{}, apperror.Wrap(apperror.CodeBackupFailed, "", err)
	}
	return backup.Artifact{RelativePath: clean, SHA256: hex.EncodeToString(hash.Sum(nil)), SizeBytes: written}, nil
}

func (s *Storage) OpenVerified(ctx context.Context, relativePath, expectedSHA256 string, expectedSize int64) (io.ReadSeekCloser, error) {
	if ctx == nil {
		return nil, errors.New("context is required")
	}
	if err := validateDigest(expectedSHA256); err != nil {
		return nil, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	if expectedSize < 0 {
		return nil, apperror.New(apperror.CodeValidationError, "backup size cannot be negative")
	}
	_, target, err := s.resolveExisting(relativePath)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, apperror.New(apperror.CodeBackupNotFound, "")
		}
		return nil, apperror.Wrap(apperror.CodeBackupFailed, "", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = file.Close()
		}
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, apperror.New(apperror.CodeBackupIntegrityFailed, "")
	}
	if info.Size() != expectedSize || info.Size() > s.maxFileBytes {
		return nil, apperror.New(apperror.CodeBackupIntegrityFailed, "")
	}
	hash := sha256.New()
	if _, err := copyWithContext(ctx, hash, file, s.maxFileBytes); err != nil {
		if apperror.IsCode(err, apperror.CodeResultTooLarge) {
			return nil, apperror.New(apperror.CodeBackupIntegrityFailed, "")
		}
		return nil, err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(actual), []byte(strings.ToLower(expectedSHA256))) != 1 {
		return nil, apperror.New(apperror.CodeBackupIntegrityFailed, "")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, apperror.Wrap(apperror.CodeBackupFailed, "", err)
	}
	ok = true
	return file, nil
}

func (s *Storage) Verify(ctx context.Context, relativePath, expectedSHA256 string, expectedSize int64) error {
	file, err := s.OpenVerified(ctx, relativePath, expectedSHA256, expectedSize)
	if err != nil {
		return err
	}
	return file.Close()
}

func (s *Storage) Delete(ctx context.Context, relativePath string) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, target, err := s.resolveExisting(relativePath)
	if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return apperror.New(apperror.CodeBackupNotFound, "")
		}
		return apperror.Wrap(apperror.CodeBackupFailed, "", err)
	}
	return syncDirectory(filepath.Dir(target))
}

func (s *Storage) CheckReady(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(s.root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("backup root directory is unavailable or has unsafe permissions")
	}
	probe, err := os.OpenFile(filepath.Join(s.root, temporaryName()), os.O_CREATE|os.O_EXCL|os.O_WRONLY, backupFileMode)
	if err != nil {
		return fmt.Errorf("backup root is not writable: %w", err)
	}
	name := probe.Name()
	if _, err := probe.Write([]byte("ready")); err != nil {
		_ = probe.Close()
		_ = os.Remove(name)
		return err
	}
	if err := probe.Sync(); err != nil {
		_ = probe.Close()
		_ = os.Remove(name)
		return err
	}
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}

func (s *Storage) resolveForWrite(relativePath string) (string, string, error) {
	if s == nil || s.root == "" {
		return "", "", errors.New("backup storage is not initialized")
	}
	clean, err := validateRelativePath(relativePath)
	if err != nil {
		return "", "", apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	target := filepath.Join(s.root, filepath.FromSlash(clean))
	if !withinRoot(s.root, target) {
		return "", "", apperror.New(apperror.CodeValidationError, "backup path escapes root")
	}
	return clean, target, nil
}

func (s *Storage) resolveExisting(relativePath string) (string, string, error) {
	clean, target, err := s.resolveForWrite(relativePath)
	if err != nil {
		return "", "", err
	}
	current := s.root
	for _, segment := range strings.Split(clean, "/") {
		current = filepath.Join(current, segment)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				return "", "", apperror.New(apperror.CodeBackupNotFound, "")
			}
			return "", "", apperror.Wrap(apperror.CodeBackupFailed, "", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", apperror.New(apperror.CodeBackupIntegrityFailed, "")
		}
	}
	return clean, target, nil
}

func validateRelativePath(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, "\\\x00\r\n") {
		return "", errors.New("backup path is invalid")
	}
	if filepath.IsAbs(value) || strings.HasPrefix(value, "/") {
		return "", errors.New("backup path must be relative")
	}
	parts := strings.Split(value, "/")
	if len(parts) == 0 {
		return "", errors.New("backup path is invalid")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > 255 {
			return "", errors.New("backup path contains an unsafe segment")
		}
	}
	clean := strings.Join(parts, "/")
	if clean != value || len(clean) > 1024 {
		return "", errors.New("backup path is not canonical")
	}
	return clean, nil
}

func ensureSecureDirectory(directory, root string) error {
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return apperror.New(apperror.CodeValidationError, "backup directory escapes root")
	}
	current := root
	if relative == "." {
		return nil
	}
	for _, segment := range strings.Split(relative, string(os.PathSeparator)) {
		current = filepath.Join(current, segment)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			if err := os.Mkdir(current, rootDirectoryMode); err != nil && !errors.Is(err, os.ErrExist) {
				return apperror.Wrap(apperror.CodeBackupFailed, "", err)
			}
			info, statErr = os.Lstat(current)
		}
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return apperror.New(apperror.CodeBackupIntegrityFailed, "")
		}
		if err := os.Chmod(current, rootDirectoryMode); err != nil {
			return apperror.Wrap(apperror.CodeBackupFailed, "", err)
		}
	}
	return nil
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader, max int64) (int64, error) {
	buffer := make([]byte, 32*1024)
	limited := &io.LimitedReader{R: source, N: max + 1}
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		count, readErr := limited.Read(buffer)
		if count > 0 {
			written, writeErr := destination.Write(buffer[:count])
			total += int64(written)
			if writeErr != nil {
				return total, apperror.Wrap(apperror.CodeBackupFailed, "", writeErr)
			}
			if written != count {
				return total, apperror.Wrap(apperror.CodeBackupFailed, "", io.ErrShortWrite)
			}
			if total > max {
				return total, apperror.New(apperror.CodeResultTooLarge, "backup exceeds configured file limit")
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, apperror.Wrap(apperror.CodeBackupFailed, "", readErr)
		}
	}
}

func validateDigest(value string) error {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return errors.New("backup SHA-256 must be 64 lowercase hexadecimal characters")
	}
	_, err := hex.DecodeString(value)
	return err
}

func withinRoot(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func temporaryName() string {
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])
	return ".backup-tmp-" + hex.EncodeToString(bytes[:])
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

var _ backup.Storage = (*Storage)(nil)

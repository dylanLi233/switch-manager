// Package backup defines vendor-neutral configuration backup storage contracts.
package backup

import (
	"context"
	"io"
)

// Artifact is the immutable metadata returned after a successful write.
// RelativePath is always slash-separated and relative to the configured root.
type Artifact struct {
	RelativePath string `json:"relative_path"`
	SHA256       string `json:"sha256"`
	SizeBytes    int64  `json:"size_bytes"`
}

// Storage persists opaque configuration backup bytes. Implementations must not
// interpret or modify device configuration content.
type Storage interface {
	Put(context.Context, string, io.Reader) (Artifact, error)
	OpenVerified(context.Context, string, string, int64) (io.ReadSeekCloser, error)
	Verify(context.Context, string, string, int64) error
	Delete(context.Context, string) error
	CheckReady(context.Context) error
}

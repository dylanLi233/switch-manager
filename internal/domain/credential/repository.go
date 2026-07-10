package credential

import (
	"context"
	"time"
)

// Metadata is the non-secret credential view safe for normal read APIs.
type Metadata struct {
	ID         string
	Name       string
	Type       Type
	Username   string
	KeyVersion string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Repository manages encrypted credentials and safe metadata views.
type Repository interface {
	Create(context.Context, Credential) (Metadata, error)
	GetMetadata(context.Context, string) (Metadata, error)
	ListMetadata(context.Context, int, int) ([]Metadata, error)
	Update(context.Context, Credential) (Metadata, error)
	SoftDelete(context.Context, string) error
}

// ExecutionRepository is the only repository contract that returns encrypted
// authentication material. It is intended for the short-lived SSH execution path.
type ExecutionRepository interface {
	GetForExecution(context.Context, string) (Credential, error)
}

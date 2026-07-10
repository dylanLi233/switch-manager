// Package credential defines encrypted SSH credential metadata.
package credential

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Type identifies the supported SSH authentication material.
type Type string

const (
	TypePassword      Type = "PASSWORD"
	TypeSSHPrivateKey Type = "SSH_PRIVATE_KEY"
)

// Validate reports whether the credential type is supported.
func (t Type) Validate() error {
	switch t {
	case TypePassword, TypeSSHPrivateKey:
		return nil
	default:
		return fmt.Errorf("unsupported credential type %q", t)
	}
}

// Credential stores encrypted authentication material only.
type Credential struct {
	ID                  string
	Name                string
	Type                Type
	Username            string
	EncryptedSecret     []byte
	EncryptedPrivateKey []byte
	EncryptedPassphrase []byte
	KeyVersion          string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// Validate enforces credential metadata and encrypted material invariants.
func (c Credential) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return errors.New("credential ID is required")
	}
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("credential name is required")
	}
	if strings.TrimSpace(c.Username) == "" {
		return errors.New("credential username is required")
	}
	if strings.TrimSpace(c.KeyVersion) == "" {
		return errors.New("credential key version is required")
	}
	if err := c.Type.Validate(); err != nil {
		return err
	}
	switch c.Type {
	case TypePassword:
		if len(c.EncryptedSecret) == 0 {
			return errors.New("encrypted password secret is required")
		}
		if len(c.EncryptedPrivateKey) != 0 {
			return errors.New("password credential cannot contain a private key")
		}
	case TypeSSHPrivateKey:
		if len(c.EncryptedPrivateKey) == 0 {
			return errors.New("encrypted private key is required")
		}
		if len(c.EncryptedSecret) != 0 {
			return errors.New("private key credential cannot contain a password secret")
		}
	}
	if !c.CreatedAt.IsZero() && !c.UpdatedAt.IsZero() && c.UpdatedAt.Before(c.CreatedAt) {
		return errors.New("credential updated time cannot precede created time")
	}
	return nil
}

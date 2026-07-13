// Package device defines managed switch identity and lifecycle rules.
package device

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Vendor identifies a supported switch vendor.
type Vendor string

const (
	VendorHuawei Vendor = "HUAWEI"
	VendorH3C    Vendor = "H3C"
)

// Validate reports whether the vendor is supported by V1.
func (v Vendor) Validate() error {
	switch v {
	case VendorHuawei, VendorH3C:
		return nil
	default:
		return fmt.Errorf("unsupported vendor %q", v)
	}
}

// DetectMode controls whether device identity is automatically detected.
type DetectMode string

const (
	DetectModeAuto   DetectMode = "AUTO"
	DetectModeManual DetectMode = "MANUAL"
)

// Validate reports whether the detection mode is supported.
func (m DetectMode) Validate() error {
	switch m {
	case DetectModeAuto, DetectModeManual:
		return nil
	default:
		return fmt.Errorf("unsupported detect mode %q", m)
	}
}

// IdentityStatus records the result of device identity verification.
type IdentityStatus string

const (
	IdentityUnknown     IdentityStatus = "UNKNOWN"
	IdentityVerified    IdentityStatus = "VERIFIED"
	IdentityMismatch    IdentityStatus = "MISMATCH"
	IdentityUnsupported IdentityStatus = "UNSUPPORTED"
)

// Validate reports whether the identity status is known.
func (s IdentityStatus) Validate() error {
	switch s {
	case IdentityUnknown, IdentityVerified, IdentityMismatch, IdentityUnsupported:
		return nil
	default:
		return fmt.Errorf("unsupported identity status %q", s)
	}
}

// Status represents whether a managed device can be contacted by the service.
type Status string

const (
	StatusActive      Status = "ACTIVE"
	StatusDisabled    Status = "DISABLED"
	StatusUnreachable Status = "UNREACHABLE"
)

// Validate reports whether the device status is known.
func (s Status) Validate() error {
	switch s {
	case StatusActive, StatusDisabled, StatusUnreachable:
		return nil
	default:
		return fmt.Errorf("unsupported device status %q", s)
	}
}

var (
	// ErrConfigurationBlocked is returned when device state makes configuration unsafe.
	ErrConfigurationBlocked = errors.New("device configuration is blocked")
)

// Device is a managed physical switch.
type Device struct {
	ID              string
	Name            string
	Host            string
	SSHPort         int
	CredentialID    string
	Vendor          Vendor
	Model           string
	OSVersion       string
	DetectMode      DetectMode
	IdentityStatus  IdentityStatus
	Status          Status
	LastConnectedAt *time.Time
	LastDetectedAt  *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ValidateHost accepts an IP literal or a conservative DNS hostname. URL,
// path, whitespace, and host:port forms are deliberately rejected because the
// port is stored separately.
func ValidateHost(host string) error {
	value := strings.TrimSpace(host)
	if value == "" {
		return errors.New("device host is required")
	}
	if value != host || len(value) > 253 || strings.ContainsAny(value, " \t\r\n/\\") || strings.Contains(value, "://") {
		return errors.New("device host is invalid")
	}
	if net.ParseIP(value) != nil {
		return nil
	}
	value = strings.TrimSuffix(value, ".")
	if value == "" {
		return errors.New("device host is invalid")
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("device host is invalid")
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return errors.New("device host is invalid")
		}
	}
	return nil
}

// Validate enforces device invariants independent of persistence.
func (d Device) Validate() error {
	if strings.TrimSpace(d.ID) == "" {
		return errors.New("device ID is required")
	}
	if strings.TrimSpace(d.Name) == "" {
		return errors.New("device name is required")
	}
	if err := ValidateHost(d.Host); err != nil {
		return err
	}
	if d.SSHPort < 1 || d.SSHPort > 65535 {
		return fmt.Errorf("SSH port %d is outside 1-65535", d.SSHPort)
	}
	if strings.TrimSpace(d.CredentialID) == "" {
		return errors.New("credential ID is required")
	}
	if err := d.Vendor.Validate(); err != nil {
		return fmt.Errorf("validate vendor: %w", err)
	}
	if err := d.DetectMode.Validate(); err != nil {
		return fmt.Errorf("validate detect mode: %w", err)
	}
	if err := d.IdentityStatus.Validate(); err != nil {
		return fmt.Errorf("validate identity status: %w", err)
	}
	if err := d.Status.Validate(); err != nil {
		return fmt.Errorf("validate device status: %w", err)
	}
	if !d.CreatedAt.IsZero() && !d.UpdatedAt.IsZero() && d.UpdatedAt.Before(d.CreatedAt) {
		return errors.New("device updated time cannot precede created time")
	}
	return nil
}

// CanConfigure reports whether configuration operations may run against the device.
func (d Device) CanConfigure() error {
	if err := d.Validate(); err != nil {
		return err
	}
	if d.Status != StatusActive {
		return fmt.Errorf("%w: device status is %s", ErrConfigurationBlocked, d.Status)
	}
	if d.IdentityStatus != IdentityVerified {
		return fmt.Errorf("%w: identity status is %s", ErrConfigurationBlocked, d.IdentityStatus)
	}
	return nil
}

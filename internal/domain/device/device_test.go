package device

import (
	"errors"
	"testing"
	"time"
)

func validDevice() Device {
	now := time.Now().UTC()
	return Device{
		ID:             "sw-1",
		Name:           "access-1",
		Host:           "192.0.2.10",
		SSHPort:        22,
		CredentialID:   "cred-1",
		Vendor:         VendorHuawei,
		DetectMode:     DetectModeAuto,
		IdentityStatus: IdentityVerified,
		Status:         StatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func TestDeviceValidate(t *testing.T) {
	t.Parallel()
	if err := validDevice().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Device)
	}{
		{"missing host", func(d *Device) { d.Host = "" }},
		{"invalid port zero", func(d *Device) { d.SSHPort = 0 }},
		{"invalid port high", func(d *Device) { d.SSHPort = 65536 }},
		{"missing credential", func(d *Device) { d.CredentialID = "" }},
		{"unknown vendor", func(d *Device) { d.Vendor = Vendor("CISCO") }},
		{"updated before created", func(d *Device) { d.UpdatedAt = d.CreatedAt.Add(-time.Second) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := validDevice()
			tc.mutate(&d)
			if err := d.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestDeviceCanConfigure(t *testing.T) {
	t.Parallel()
	if err := validDevice().CanConfigure(); err != nil {
		t.Fatalf("CanConfigure() error = %v", err)
	}

	for _, mutate := range []func(*Device){
		func(d *Device) { d.Status = StatusDisabled },
		func(d *Device) { d.Status = StatusUnreachable },
		func(d *Device) { d.IdentityStatus = IdentityMismatch },
		func(d *Device) { d.IdentityStatus = IdentityUnsupported },
		func(d *Device) { d.IdentityStatus = IdentityUnknown },
	} {
		d := validDevice()
		mutate(&d)
		if err := d.CanConfigure(); !errors.Is(err, ErrConfigurationBlocked) {
			t.Fatalf("CanConfigure() error = %v, want ErrConfigurationBlocked", err)
		}
	}
}

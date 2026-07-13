// Package switchinterface defines vendor-neutral managed interface views.
package switchinterface

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/dylanLi233/switch-manager/internal/domain/vlan"
)

type AdminState string

const (
	AdminEnabled  AdminState = "ENABLED"
	AdminDisabled AdminState = "DISABLED"
)

func (s AdminState) Validate() error {
	switch s {
	case AdminEnabled, AdminDisabled:
		return nil
	default:
		return fmt.Errorf("unsupported interface admin state %q", s)
	}
}

type OperState string

const (
	OperUp      OperState = "UP"
	OperDown    OperState = "DOWN"
	OperUnknown OperState = "UNKNOWN"
)

func (s OperState) Validate() error {
	switch s {
	case OperUp, OperDown, OperUnknown:
		return nil
	default:
		return fmt.Errorf("unsupported interface operational state %q", s)
	}
}

type Mode string

const (
	ModeAccess  Mode = "ACCESS"
	ModeTrunk   Mode = "TRUNK"
	ModeUnknown Mode = "UNKNOWN"
)

func (m Mode) Validate() error {
	switch m {
	case ModeAccess, ModeTrunk, ModeUnknown:
		return nil
	default:
		return fmt.Errorf("unsupported interface mode %q", m)
	}
}

// Interface is the normalized public interface contract.
type Interface struct {
	Name         string     `json:"name"`
	Description  string     `json:"description,omitempty"`
	AdminState   AdminState `json:"admin_state"`
	OperState    OperState  `json:"oper_state"`
	Mode         Mode       `json:"mode"`
	AccessVLAN   int        `json:"access_vlan,omitempty"`
	NativeVLAN   int        `json:"native_vlan,omitempty"`
	AllowedVLANs []int      `json:"allowed_vlans,omitempty"`
}

// ValidateNameSafety performs only vendor-neutral safety checks. Concrete name
// syntax belongs to the selected plugin's InterfaceNameValidator.
func ValidateNameSafety(name string) error {
	if name == "" || strings.TrimSpace(name) != name {
		return errors.New("interface name is required without surrounding whitespace")
	}
	if !utf8.ValidString(name) || len(name) > 128 {
		return errors.New("interface name must be valid UTF-8 up to 128 bytes")
	}
	if strings.ContainsAny(name, "\r\n\x00") {
		return errors.New("interface name contains a forbidden control character")
	}
	return nil
}

func NormalizeVLANs(values []int, requireNonEmpty bool) ([]int, error) {
	if requireNonEmpty && len(values) == 0 {
		return nil, errors.New("at least one VLAN is required")
	}
	seen := make(map[int]struct{}, len(values))
	result := make([]int, 0, len(values))
	for _, value := range values {
		if err := vlan.ValidateID(value); err != nil {
			return nil, err
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("duplicate VLAN %d", value)
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Ints(result)
	return result, nil
}

func (i Interface) Validate() error {
	if err := ValidateNameSafety(i.Name); err != nil {
		return err
	}
	if err := i.AdminState.Validate(); err != nil {
		return err
	}
	if err := i.OperState.Validate(); err != nil {
		return err
	}
	if err := i.Mode.Validate(); err != nil {
		return err
	}
	allowed, err := NormalizeVLANs(i.AllowedVLANs, false)
	if err != nil {
		return err
	}
	if len(allowed) != len(i.AllowedVLANs) {
		return errors.New("allowed VLAN normalization failed")
	}
	switch i.Mode {
	case ModeAccess:
		if err := vlan.ValidateID(i.AccessVLAN); err != nil {
			return fmt.Errorf("validate access VLAN: %w", err)
		}
		if i.NativeVLAN != 0 || len(i.AllowedVLANs) != 0 {
			return errors.New("access interface cannot contain trunk VLAN fields")
		}
	case ModeTrunk:
		if i.AccessVLAN != 0 {
			return errors.New("trunk interface cannot contain access_vlan")
		}
		if _, err := NormalizeVLANs(i.AllowedVLANs, true); err != nil {
			return fmt.Errorf("validate allowed VLANs: %w", err)
		}
		if i.NativeVLAN != 0 {
			if err := vlan.ValidateID(i.NativeVLAN); err != nil {
				return fmt.Errorf("validate native VLAN: %w", err)
			}
			found := false
			for _, id := range i.AllowedVLANs {
				if id == i.NativeVLAN {
					found = true
					break
				}
			}
			if !found {
				return errors.New("native VLAN must be included in allowed VLANs")
			}
		}
	case ModeUnknown:
		if i.AccessVLAN != 0 || i.NativeVLAN != 0 || len(i.AllowedVLANs) != 0 {
			return errors.New("unknown mode cannot contain VLAN mode fields")
		}
	}
	return nil
}

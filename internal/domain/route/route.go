// Package route defines the vendor-neutral static-route contract.
package route

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dylanLi233/switch-manager/internal/domain/switchinterface"
)

type AddressFamily string

const (
	FamilyIPv4 AddressFamily = "IPV4"
	FamilyIPv6 AddressFamily = "IPV6"
)

func (f AddressFamily) Validate() error {
	switch f {
	case FamilyIPv4, FamilyIPv6:
		return nil
	default:
		return fmt.Errorf("unsupported route address family %q", f)
	}
}

// Spec is the stable public request contract. Vendor-specific preferences,
// track objects, VRFs, and route types are intentionally outside V1.
type Spec struct {
	AddressFamily    AddressFamily `json:"address_family"`
	Destination      string        `json:"destination"`
	NextHop          string        `json:"next_hop"`
	OutgoingInterface string       `json:"outgoing_interface,omitempty"`
	Description      string        `json:"description,omitempty"`
}

type StaticRoute struct {
	RouteID string `json:"route_id"`
	Spec
}

func ValidateID(id string) error {
	if id == "" || id != strings.TrimSpace(id) || len(id) > 64 {
		return errors.New("route_id must contain 1-64 non-whitespace characters")
	}
	for _, character := range id {
		if unicode.IsControl(character) || unicode.IsSpace(character) || character == '/' || character == '\\' {
			return errors.New("route_id contains an unsafe character")
		}
	}
	return nil
}

func NormalizeSpec(value Spec) (Spec, error) {
	if err := value.AddressFamily.Validate(); err != nil {
		return Spec{}, err
	}
	prefix, err := netip.ParsePrefix(strings.TrimSpace(value.Destination))
	if err != nil {
		return Spec{}, fmt.Errorf("destination must be a CIDR prefix: %w", err)
	}
	prefix = prefix.Masked()
	nextHop, err := netip.ParseAddr(strings.TrimSpace(value.NextHop))
	if err != nil {
		return Spec{}, fmt.Errorf("next_hop must be an IP address: %w", err)
	}
	if nextHop.IsUnspecified() || nextHop.IsMulticast() {
		return Spec{}, errors.New("next_hop cannot be unspecified or multicast")
	}
	wantIPv4 := value.AddressFamily == FamilyIPv4
	if prefix.Addr().Is4() != wantIPv4 || nextHop.Is4() != wantIPv4 {
		return Spec{}, errors.New("destination and next_hop must match address_family")
	}
	outgoing := strings.TrimSpace(value.OutgoingInterface)
	if outgoing != "" {
		if outgoing != value.OutgoingInterface {
			return Spec{}, errors.New("outgoing_interface cannot contain surrounding whitespace")
		}
		if err := switchinterface.ValidateNameSafety(outgoing); err != nil {
			return Spec{}, fmt.Errorf("outgoing_interface is unsafe: %w", err)
		}
	}
	description, err := normalizeDescription(value.Description)
	if err != nil {
		return Spec{}, err
	}
	return Spec{AddressFamily: value.AddressFamily, Destination: prefix.String(), NextHop: nextHop.String(), OutgoingInterface: outgoing, Description: description}, nil
}

func (r StaticRoute) Validate() error {
	if err := ValidateID(r.RouteID); err != nil {
		return err
	}
	normalized, err := NormalizeSpec(r.Spec)
	if err != nil {
		return err
	}
	if normalized != r.Spec {
		return errors.New("route fields are not normalized")
	}
	return nil
}

func normalizeDescription(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", errors.New("description must be valid UTF-8")
	}
	if value != strings.TrimSpace(value) || len(value) > 128 {
		return "", errors.New("description must be at most 128 bytes without surrounding whitespace")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", errors.New("description contains a control character")
		}
	}
	return value, nil
}

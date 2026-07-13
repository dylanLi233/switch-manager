// Package acl defines the explicitly experimental ACL contract used by TASK-015.
package acl

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const ExperimentalSchemaVersion = "experimental-v1"

type AddressFamily string

const (
	FamilyAny  AddressFamily = "ANY"
	FamilyIPv4 AddressFamily = "IPV4"
	FamilyIPv6 AddressFamily = "IPV6"
)

func (f AddressFamily) Validate() error {
	switch f {
	case FamilyAny, FamilyIPv4, FamilyIPv6:
		return nil
	default:
		return fmt.Errorf("unsupported ACL address family %q", f)
	}
}

type Action string

const (
	ActionPermit Action = "PERMIT"
	ActionDeny   Action = "DENY"
)

func (a Action) Validate() error {
	switch a {
	case ActionPermit, ActionDeny:
		return nil
	default:
		return fmt.Errorf("unsupported ACL action %q", a)
	}
}

type Protocol string

const (
	ProtocolAny  Protocol = "ANY"
	ProtocolTCP  Protocol = "TCP"
	ProtocolUDP  Protocol = "UDP"
	ProtocolICMP Protocol = "ICMP"
)

func (p Protocol) Validate() error {
	switch p {
	case ProtocolAny, ProtocolTCP, ProtocolUDP, ProtocolICMP:
		return nil
	default:
		return fmt.Errorf("unsupported ACL protocol %q", p)
	}
}

type PortRange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

func (p PortRange) Validate() error {
	if p.From < 1 || p.From > 65535 || p.To < 1 || p.To > 65535 || p.From > p.To {
		return errors.New("port range must be within 1-65535 and from <= to")
	}
	return nil
}

type Rule struct {
	Sequence         int         `json:"sequence"`
	Action           Action      `json:"action"`
	Protocol         Protocol    `json:"protocol"`
	Source           string      `json:"source"`
	Destination      string      `json:"destination"`
	SourcePorts      []PortRange `json:"source_ports,omitempty"`
	DestinationPorts []PortRange `json:"destination_ports,omitempty"`
	Description      string      `json:"description,omitempty"`
}

type Spec struct {
	SchemaVersion string        `json:"schema_version"`
	Name          string        `json:"name"`
	Description   string        `json:"description,omitempty"`
	AddressFamily AddressFamily `json:"address_family"`
	Rules         []Rule        `json:"rules"`
}

type ACL struct {
	ACLID string `json:"acl_id"`
	Spec
}

func ValidateID(id string) error {
	if id == "" || id != strings.TrimSpace(id) || len(id) > 64 {
		return errors.New("acl_id must contain 1-64 non-whitespace characters")
	}
	for _, character := range id {
		if unicode.IsControl(character) || unicode.IsSpace(character) || character == '/' || character == '\\' {
			return errors.New("acl_id contains an unsafe character")
		}
	}
	return nil
}

// ValidateNameSafety intentionally does not impose a Huawei/H3C naming syntax.
// Real plugin implementations own the final vendor-specific validator.
func ValidateNameSafety(name string) error {
	if !utf8.ValidString(name) || name == "" || name != strings.TrimSpace(name) || len(name) > 64 {
		return errors.New("ACL name must be valid UTF-8, 1-64 bytes, and have no surrounding whitespace")
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return errors.New("ACL name contains a control character")
		}
	}
	return nil
}

func NormalizeSpec(value Spec) (Spec, error) {
	if value.SchemaVersion != ExperimentalSchemaVersion {
		return Spec{}, fmt.Errorf("schema_version must be %q", ExperimentalSchemaVersion)
	}
	if err := ValidateNameSafety(value.Name); err != nil {
		return Spec{}, err
	}
	if err := value.AddressFamily.Validate(); err != nil {
		return Spec{}, err
	}
	description, err := normalizeDescription(value.Description)
	if err != nil {
		return Spec{}, err
	}
	if len(value.Rules) == 0 || len(value.Rules) > 256 {
		return Spec{}, errors.New("ACL must contain 1-256 rules")
	}
	rules := append([]Rule(nil), value.Rules...)
	seen := make(map[int]struct{}, len(rules))
	for index := range rules {
		normalized, err := normalizeRule(rules[index], value.AddressFamily)
		if err != nil {
			return Spec{}, fmt.Errorf("rule %d: %w", index, err)
		}
		if _, exists := seen[normalized.Sequence]; exists {
			return Spec{}, fmt.Errorf("duplicate ACL sequence %d", normalized.Sequence)
		}
		seen[normalized.Sequence] = struct{}{}
		rules[index] = normalized
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Sequence < rules[j].Sequence })
	return Spec{SchemaVersion: ExperimentalSchemaVersion, Name: value.Name, Description: description, AddressFamily: value.AddressFamily, Rules: rules}, nil
}

func (a ACL) Validate() error {
	if err := ValidateID(a.ACLID); err != nil {
		return err
	}
	normalized, err := NormalizeSpec(a.Spec)
	if err != nil {
		return err
	}
	if normalized.SchemaVersion != a.SchemaVersion || normalized.Name != a.Name || normalized.Description != a.Description || normalized.AddressFamily != a.AddressFamily || len(normalized.Rules) != len(a.Rules) {
		return errors.New("ACL fields are not normalized")
	}
	for index := range normalized.Rules {
		if !rulesEqual(normalized.Rules[index], a.Rules[index]) {
			return errors.New("ACL rules are not normalized")
		}
	}
	return nil
}

func normalizeRule(value Rule, family AddressFamily) (Rule, error) {
	if value.Sequence < 1 || value.Sequence > 65535 {
		return Rule{}, errors.New("sequence must be within 1-65535")
	}
	if err := value.Action.Validate(); err != nil {
		return Rule{}, err
	}
	if err := value.Protocol.Validate(); err != nil {
		return Rule{}, err
	}
	source, err := normalizeEndpoint(value.Source, family)
	if err != nil {
		return Rule{}, fmt.Errorf("source: %w", err)
	}
	destination, err := normalizeEndpoint(value.Destination, family)
	if err != nil {
		return Rule{}, fmt.Errorf("destination: %w", err)
	}
	if value.Protocol != ProtocolTCP && value.Protocol != ProtocolUDP && (len(value.SourcePorts) > 0 || len(value.DestinationPorts) > 0) {
		return Rule{}, errors.New("ports are only valid for TCP or UDP rules")
	}
	sourcePorts, err := normalizePorts(value.SourcePorts)
	if err != nil {
		return Rule{}, fmt.Errorf("source_ports: %w", err)
	}
	destinationPorts, err := normalizePorts(value.DestinationPorts)
	if err != nil {
		return Rule{}, fmt.Errorf("destination_ports: %w", err)
	}
	description, err := normalizeDescription(value.Description)
	if err != nil {
		return Rule{}, err
	}
	return Rule{Sequence: value.Sequence, Action: value.Action, Protocol: value.Protocol, Source: source, Destination: destination, SourcePorts: sourcePorts, DestinationPorts: destinationPorts, Description: description}, nil
}

func normalizeEndpoint(value string, family AddressFamily) (string, error) {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "any") {
		return "any", nil
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return "", errors.New("endpoint must be 'any' or a CIDR prefix")
	}
	prefix = prefix.Masked()
	if family == FamilyIPv4 && !prefix.Addr().Is4() {
		return "", errors.New("endpoint must be IPv4")
	}
	if family == FamilyIPv6 && prefix.Addr().Is4() {
		return "", errors.New("endpoint must be IPv6")
	}
	return prefix.String(), nil
}

func normalizePorts(values []PortRange) ([]PortRange, error) {
	result := append([]PortRange(nil), values...)
	for _, value := range result {
		if err := value.Validate(); err != nil {
			return nil, err
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].From == result[j].From {
			return result[i].To < result[j].To
		}
		return result[i].From < result[j].From
	})
	for index := 1; index < len(result); index++ {
		if result[index].From <= result[index-1].To {
			return nil, errors.New("port ranges cannot overlap")
		}
	}
	return result, nil
}

func normalizeDescription(value string) (string, error) {
	if !utf8.ValidString(value) || value != strings.TrimSpace(value) || len(value) > 128 {
		return "", errors.New("description must be valid UTF-8, at most 128 bytes, and have no surrounding whitespace")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", errors.New("description contains a control character")
		}
	}
	return value, nil
}

func rulesEqual(left, right Rule) bool {
	if left.Sequence != right.Sequence || left.Action != right.Action || left.Protocol != right.Protocol || left.Source != right.Source || left.Destination != right.Destination || left.Description != right.Description || len(left.SourcePorts) != len(right.SourcePorts) || len(left.DestinationPorts) != len(right.DestinationPorts) {
		return false
	}
	for index := range left.SourcePorts {
		if left.SourcePorts[index] != right.SourcePorts[index] {
			return false
		}
	}
	for index := range left.DestinationPorts {
		if left.DestinationPorts[index] != right.DestinationPorts[index] {
			return false
		}
	}
	return true
}

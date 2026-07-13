// Package telemetry defines normalized read-only switch telemetry contracts.
package telemetry

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/switchinterface"
	"github.com/dylanLi233/switch-manager/internal/domain/vlan"
)

type MACEntryType string

const (
	MACDynamic MACEntryType = "DYNAMIC"
	MACStatic  MACEntryType = "STATIC"
)

func (t MACEntryType) Validate() error {
	switch t {
	case MACDynamic, MACStatic:
		return nil
	default:
		return fmt.Errorf("unsupported MAC entry type %q", t)
	}
}

type MACEntry struct {
	MACAddress    string       `json:"mac_address"`
	VLANID        int          `json:"vlan_id"`
	InterfaceName string       `json:"interface_name"`
	EntryType     MACEntryType `json:"entry_type"`
	AgeSeconds    int64        `json:"age_seconds"`
}

func NormalizeMACEntry(value MACEntry) (MACEntry, error) {
	address, err := normalizeMAC(value.MACAddress)
	if err != nil {
		return MACEntry{}, err
	}
	if err := vlan.ValidateID(value.VLANID); err != nil {
		return MACEntry{}, err
	}
	if err := switchinterface.ValidateNameSafety(value.InterfaceName); err != nil {
		return MACEntry{}, err
	}
	if err := value.EntryType.Validate(); err != nil {
		return MACEntry{}, err
	}
	if value.AgeSeconds < 0 {
		return MACEntry{}, errors.New("MAC age_seconds cannot be negative")
	}
	value.MACAddress = address
	return value, nil
}

type ARPState string

const (
	ARPReachable  ARPState = "REACHABLE"
	ARPStale      ARPState = "STALE"
	ARPIncomplete ARPState = "INCOMPLETE"
	ARPStatic     ARPState = "STATIC"
	ARPUnknown    ARPState = "UNKNOWN"
)

func (s ARPState) Validate() error {
	switch s {
	case ARPReachable, ARPStale, ARPIncomplete, ARPStatic, ARPUnknown:
		return nil
	default:
		return fmt.Errorf("unsupported ARP state %q", s)
	}
}

type ARPEntry struct {
	IPAddress     string   `json:"ip_address"`
	MACAddress    string   `json:"mac_address,omitempty"`
	InterfaceName string   `json:"interface_name"`
	State         ARPState `json:"state"`
	AgeSeconds    int64    `json:"age_seconds"`
}

func NormalizeARPEntry(value ARPEntry) (ARPEntry, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(value.IPAddress))
	if err != nil {
		return ARPEntry{}, fmt.Errorf("invalid ARP IP address: %w", err)
	}
	if !address.Is4() {
		return ARPEntry{}, errors.New("ARP IP address must be IPv4; IPv6 neighbor discovery is a separate capability")
	}
	if address.IsUnspecified() || address.IsMulticast() {
		return ARPEntry{}, errors.New("ARP IP address cannot be unspecified or multicast")
	}
	if err := switchinterface.ValidateNameSafety(value.InterfaceName); err != nil {
		return ARPEntry{}, err
	}
	if err := value.State.Validate(); err != nil {
		return ARPEntry{}, err
	}
	if value.AgeSeconds < 0 {
		return ARPEntry{}, errors.New("ARP age_seconds cannot be negative")
	}
	if value.State == ARPIncomplete {
		if strings.TrimSpace(value.MACAddress) != "" {
			return ARPEntry{}, errors.New("incomplete ARP entry cannot contain a MAC address")
		}
		value.MACAddress = ""
	} else {
		value.MACAddress, err = normalizeMAC(value.MACAddress)
		if err != nil {
			return ARPEntry{}, err
		}
	}
	value.IPAddress = address.String()
	return value, nil
}

type HealthState string

const (
	HealthHealthy  HealthState = "HEALTHY"
	HealthDegraded HealthState = "DEGRADED"
	HealthCritical HealthState = "CRITICAL"
	HealthUnknown  HealthState = "UNKNOWN"
)

func (s HealthState) Validate() error {
	switch s {
	case HealthHealthy, HealthDegraded, HealthCritical, HealthUnknown:
		return nil
	default:
		return fmt.Errorf("unsupported health state %q", s)
	}
}

type DeviceStatus struct {
	Hostname           string      `json:"hostname"`
	UptimeSeconds      int64       `json:"uptime_seconds"`
	HealthState        HealthState `json:"health_state"`
	CPUUsagePercent    *float64    `json:"cpu_usage_percent,omitempty"`
	MemoryUsagePercent *float64    `json:"memory_usage_percent,omitempty"`
	TemperatureCelsius *float64    `json:"temperature_celsius,omitempty"`
	ActiveAlarms       []string    `json:"active_alarms"`
	CollectedAt        time.Time   `json:"collected_at"`
}

func NormalizeDeviceStatus(value DeviceStatus) (DeviceStatus, error) {
	value.Hostname = strings.TrimSpace(value.Hostname)
	if value.Hostname == "" || len(value.Hostname) > 253 || strings.ContainsAny(value.Hostname, "\r\n\x00") {
		return DeviceStatus{}, errors.New("device status hostname is invalid")
	}
	if value.UptimeSeconds < 0 {
		return DeviceStatus{}, errors.New("device uptime cannot be negative")
	}
	if err := value.HealthState.Validate(); err != nil {
		return DeviceStatus{}, err
	}
	if err := validatePercent("cpu_usage_percent", value.CPUUsagePercent); err != nil {
		return DeviceStatus{}, err
	}
	if err := validatePercent("memory_usage_percent", value.MemoryUsagePercent); err != nil {
		return DeviceStatus{}, err
	}
	if value.TemperatureCelsius != nil && (*value.TemperatureCelsius < -100 || *value.TemperatureCelsius > 250) {
		return DeviceStatus{}, errors.New("temperature_celsius is outside the supported range")
	}
	if value.CollectedAt.IsZero() {
		return DeviceStatus{}, errors.New("collected_at is required")
	}
	if len(value.ActiveAlarms) > 256 {
		return DeviceStatus{}, errors.New("too many active alarms")
	}
	alarms := make([]string, len(value.ActiveAlarms))
	for index, alarm := range value.ActiveAlarms {
		alarm = strings.TrimSpace(alarm)
		if alarm == "" || len(alarm) > 256 || strings.ContainsAny(alarm, "\r\n\x00") {
			return DeviceStatus{}, fmt.Errorf("active alarm %d is invalid", index)
		}
		alarms[index] = alarm
	}
	value.ActiveAlarms = alarms
	value.CollectedAt = value.CollectedAt.UTC()
	return value, nil
}

type MACPage struct {
	Entries  []MACEntry `json:"entries"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	Total    int        `json:"total"`
}

type ARPPage struct {
	Entries  []ARPEntry `json:"entries"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	Total    int        `json:"total"`
}

func normalizeMAC(raw string) (string, error) {
	address, err := net.ParseMAC(strings.TrimSpace(raw))
	if err != nil || len(address) != 6 {
		return "", errors.New("MAC address must be a 48-bit address")
	}
	return address.String(), nil
}

func validatePercent(name string, value *float64) error {
	if value != nil && (*value < 0 || *value > 100) {
		return fmt.Errorf("%s must be between 0 and 100", name)
	}
	return nil
}

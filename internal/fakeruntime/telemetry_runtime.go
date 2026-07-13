package fakeruntime

import (
	"errors"
	"strings"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/telemetry"
)

type runtimeTableQuery struct {
	Page        int `json:"page"`
	PageSize    int `json:"page_size"`
	ResultLimit int `json:"result_limit"`
}

func (f *Factory) ensureTelemetryLocked(deviceID string) {
	if _, exists := f.macEntries[deviceID]; !exists {
		f.macEntries[deviceID] = []telemetry.MACEntry{
			{MACAddress: "00:11:22:33:44:55", VLANID: 1, InterfaceName: "FakeEthernet1/0/1", EntryType: telemetry.MACDynamic, AgeSeconds: 120},
			{MACAddress: "00:11:22:33:44:66", VLANID: 1, InterfaceName: "FakeEthernet1/0/2", EntryType: telemetry.MACStatic, AgeSeconds: 0},
		}
	}
	if _, exists := f.arpEntries[deviceID]; !exists {
		f.arpEntries[deviceID] = []telemetry.ARPEntry{
			{IPAddress: "192.0.2.10", MACAddress: "00:11:22:33:44:55", InterfaceName: "FakeEthernet1/0/1", State: telemetry.ARPReachable, AgeSeconds: 30},
			{IPAddress: "198.51.100.10", MACAddress: "00:11:22:33:44:66", InterfaceName: "FakeEthernet1/0/2", State: telemetry.ARPStale, AgeSeconds: 300},
		}
	}
	if _, exists := f.statuses[deviceID]; !exists {
		cpu, memory, temperature := 25.0, 40.0, 42.5
		f.statuses[deviceID] = telemetry.DeviceStatus{
			Hostname: "fake-switch", UptimeSeconds: 86400, HealthState: telemetry.HealthHealthy,
			CPUUsagePercent: &cpu, MemoryUsagePercent: &memory, TemperatureCelsius: &temperature,
			ActiveAlarms: []string{}, CollectedAt: time.Now().UTC(),
		}
	}
}

func (f *Factory) SnapshotMACEntries(deviceID string) []telemetry.MACEntry {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	result := append([]telemetry.MACEntry(nil), f.macEntries[deviceID]...)
	f.mu.RUnlock()
	return result
}

func (f *Factory) SnapshotARPEntries(deviceID string) []telemetry.ARPEntry {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	result := append([]telemetry.ARPEntry(nil), f.arpEntries[deviceID]...)
	f.mu.RUnlock()
	return result
}

func (f *Factory) SnapshotStatus(deviceID string) telemetry.DeviceStatus {
	if f == nil {
		return telemetry.DeviceStatus{}
	}
	f.mu.RLock()
	value := cloneStatus(f.statuses[deviceID])
	f.mu.RUnlock()
	return value
}

func (f *Factory) ReplaceMACEntries(deviceID string, entries []telemetry.MACEntry) error {
	if f == nil {
		return errors.New("fake runtime is nil")
	}
	normalized := make([]telemetry.MACEntry, len(entries))
	for index, entry := range entries {
		value, err := telemetry.NormalizeMACEntry(entry)
		if err != nil {
			return err
		}
		normalized[index] = value
	}
	f.mu.Lock()
	f.macEntries[deviceID] = normalized
	f.mu.Unlock()
	return nil
}

func (f *Factory) ReplaceARPEntries(deviceID string, entries []telemetry.ARPEntry) error {
	if f == nil {
		return errors.New("fake runtime is nil")
	}
	normalized := make([]telemetry.ARPEntry, len(entries))
	for index, entry := range entries {
		value, err := telemetry.NormalizeARPEntry(entry)
		if err != nil {
			return err
		}
		normalized[index] = value
	}
	f.mu.Lock()
	f.arpEntries[deviceID] = normalized
	f.mu.Unlock()
	return nil
}

func (f *Factory) ReplaceStatus(deviceID string, status telemetry.DeviceStatus) error {
	if f == nil {
		return errors.New("fake runtime is nil")
	}
	normalized, err := telemetry.NormalizeDeviceStatus(status)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.statuses[deviceID] = cloneStatus(normalized)
	f.mu.Unlock()
	return nil
}

func (s *session) executeTelemetry(text string) (string, error) {
	switch {
	case strings.HasPrefix(text, "fake.mac_table.list "):
		if _, err := decodeRuntimeTableQuery(strings.TrimPrefix(text, "fake.mac_table.list ")); err != nil {
			return "", commandRejected(err)
		}
		return marshal(map[string]any{"entries": s.factory.SnapshotMACEntries(s.deviceID)})
	case strings.HasPrefix(text, "fake.arp_table.list "):
		if _, err := decodeRuntimeTableQuery(strings.TrimPrefix(text, "fake.arp_table.list ")); err != nil {
			return "", commandRejected(err)
		}
		return marshal(map[string]any{"entries": s.factory.SnapshotARPEntries(s.deviceID)})
	case text == "fake.device_status.get":
		status := s.factory.SnapshotStatus(s.deviceID)
		if status.Hostname == "" {
			return "", apperror.New(apperror.CodeResourceNotFound, "")
		}
		status.CollectedAt = time.Now().UTC()
		return marshal(status)
	default:
		return "", apperror.New(apperror.CodeCommandRejected, "")
	}
}

func decodeRuntimeTableQuery(encoded string) (runtimeTableQuery, error) {
	var query runtimeTableQuery
	if err := decodeRuntimeObject(encoded, &query); err != nil {
		return runtimeTableQuery{}, err
	}
	if query.Page < 1 || query.Page > 1_000_000 || query.PageSize < 1 || query.PageSize > 500 || query.ResultLimit < 1 || query.ResultLimit > 100_000 {
		return runtimeTableQuery{}, errors.New("table query options are invalid")
	}
	return query, nil
}

func cloneStatus(value telemetry.DeviceStatus) telemetry.DeviceStatus {
	value.ActiveAlarms = append([]string(nil), value.ActiveAlarms...)
	if value.CPUUsagePercent != nil {
		copy := *value.CPUUsagePercent
		value.CPUUsagePercent = &copy
	}
	if value.MemoryUsagePercent != nil {
		copy := *value.MemoryUsagePercent
		value.MemoryUsagePercent = &copy
	}
	if value.TemperatureCelsius != nil {
		copy := *value.TemperatureCelsius
		value.TemperatureCelsius = &copy
	}
	return value
}

package telemetry

import (
	"testing"
	"time"
)

func TestNormalizeMACEntry(t *testing.T) {
	value, err := NormalizeMACEntry(MACEntry{MACAddress: "00-11-22-33-44-55", VLANID: 100, InterfaceName: "FakeEthernet1/0/1", EntryType: MACDynamic, AgeSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if value.MACAddress != "00:11:22:33:44:55" {
		t.Fatalf("mac=%q", value.MACAddress)
	}
}

func TestNormalizeARPEntry(t *testing.T) {
	value, err := NormalizeARPEntry(ARPEntry{IPAddress: "192.0.2.1", MACAddress: "00:11:22:33:44:55", InterfaceName: "FakeEthernet1/0/1", State: ARPReachable, AgeSeconds: 3})
	if err != nil {
		t.Fatal(err)
	}
	if value.IPAddress != "192.0.2.1" {
		t.Fatalf("ip=%q", value.IPAddress)
	}
	if _, err := NormalizeARPEntry(ARPEntry{IPAddress: "2001:db8::1", MACAddress: "00:11:22:33:44:55", InterfaceName: "FakeEthernet1/0/1", State: ARPReachable, AgeSeconds: 3}); err == nil {
		t.Fatal("expected IPv6 ARP rejection")
	}
	if _, err := NormalizeARPEntry(ARPEntry{IPAddress: "192.0.2.1", MACAddress: "00:11:22:33:44:55", InterfaceName: "FakeEthernet1/0/1", State: ARPIncomplete}); err == nil {
		t.Fatal("expected incomplete ARP MAC rejection")
	}
}

func TestNormalizeDeviceStatus(t *testing.T) {
	cpu, memory, temperature := 25.0, 40.0, 42.5
	value, err := NormalizeDeviceStatus(DeviceStatus{Hostname: " fake-switch ", UptimeSeconds: 10, HealthState: HealthHealthy, CPUUsagePercent: &cpu, MemoryUsagePercent: &memory, TemperatureCelsius: &temperature, ActiveAlarms: []string{}, CollectedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if value.Hostname != "fake-switch" || value.CollectedAt.Location() != time.UTC {
		t.Fatalf("status=%+v", value)
	}
	bad := 101.0
	if _, err := NormalizeDeviceStatus(DeviceStatus{Hostname: "switch", HealthState: HealthHealthy, CPUUsagePercent: &bad, CollectedAt: time.Now()}); err == nil {
		t.Fatal("expected percentage validation error")
	}
}

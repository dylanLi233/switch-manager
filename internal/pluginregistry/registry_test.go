package pluginregistry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
	"github.com/dylanLi233/switch-manager/plugins/fake"
)

func TestRegistryDuplicateVendor(t *testing.T) {
	t.Parallel()
	registry := NewCurrent()
	first, _ := fake.New(pluginapi.VendorHuawei)
	second, _ := fake.New(pluginapi.VendorHuawei)
	if err := registry.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(second); !errors.Is(err, ErrDuplicateVendor) {
		t.Fatalf("Register() error = %v", err)
	}
}

func TestRegistryIncompatibleSDK(t *testing.T) {
	t.Parallel()
	registry := NewCurrent()
	plugin := &metadataOverride{
		Plugin: mustFake(t, pluginapi.VendorHuawei),
		metadata: pluginapi.Metadata{
			Name: "future-huawei", Vendor: pluginapi.VendorHuawei,
			PluginVersion: pluginapi.Version{Major: 1, Minor: 0, Patch: 0},
			SDKVersion:    pluginapi.Version{Major: 2, Minor: 0, Patch: 0},
			Operations:    []pluginapi.OperationName{"diagnostic.echo"},
		},
	}
	if err := registry.Register(plugin); !errors.Is(err, ErrIncompatibleSDK) {
		t.Fatalf("Register() error = %v", err)
	}
}

func TestRegistryCapabilityLookup(t *testing.T) {
	t.Parallel()
	registry := NewCurrent()
	plugin := mustFake(t, pluginapi.VendorHuawei)
	if err := registry.Register(plugin); err != nil {
		t.Fatal(err)
	}
	capability, err := registry.LookupCapability(context.Background(), pluginapi.VendorHuawei, pluginapi.DeviceInfo{
		Vendor: pluginapi.VendorHuawei, Model: "UNKNOWN",
	}, fake.OperationEchoConfig)
	if err != nil {
		t.Fatal(err)
	}
	if capability.Level != pluginapi.SupportUnsupported {
		t.Fatalf("capability = %+v", capability)
	}
}

func TestRegistryConcurrentReads(t *testing.T) {
	t.Parallel()
	registry := NewCurrent()
	if err := registry.Register(mustFake(t, pluginapi.VendorHuawei)); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for i := 0; i < 20; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := registry.Resolve(pluginapi.VendorHuawei); err != nil {
				t.Errorf("Resolve() error = %v", err)
			}
			_ = registry.Metadata()
		}()
	}
	wait.Wait()
}

func TestSDKDomainAdapters(t *testing.T) {
	t.Parallel()
	sdkVendor, err := VendorFromDomain(device.VendorHuawei)
	if err != nil || sdkVendor != pluginapi.VendorHuawei {
		t.Fatalf("VendorFromDomain() = %q, %v", sdkVendor, err)
	}
	plan, err := PlanToDomain(pluginapi.ExecutionPlan{
		PlanID: "p", DeviceID: "d", Vendor: pluginapi.VendorHuawei,
		PluginName: "fake-huawei", PluginVersion: "1.0.0", Operation: "diagnostic.echo",
		Class: pluginapi.ClassQuery, RiskLevel: pluginapi.RiskLow,
		Commands: []pluginapi.PlannedCommand{{Sequence: 1, Text: "fake", Timeout: time.Second}},
	})
	if err != nil || plan.Vendor != device.VendorHuawei || plan.Class != operation.ClassQuery {
		t.Fatalf("PlanToDomain() = %+v, %v", plan, err)
	}
}

func mustFake(t *testing.T, vendor pluginapi.Vendor) *fake.Plugin {
	t.Helper()
	plugin, err := fake.New(vendor)
	if err != nil {
		t.Fatal(err)
	}
	return plugin
}

type metadataOverride struct {
	pluginapi.Plugin
	metadata pluginapi.Metadata
}

func (m *metadataOverride) Metadata() pluginapi.Metadata { return m.metadata }

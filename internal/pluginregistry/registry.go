// Package pluginregistry provides a thread-safe registry and the only adapters
// between the public plugin SDK and internal domain models.
package pluginregistry

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

var (
	ErrDuplicateVendor = errors.New("plugin vendor is already registered")
	ErrPluginNotFound  = errors.New("plugin not found")
	ErrIncompatibleSDK = errors.New("plugin SDK is incompatible")
)

type entry struct {
	plugin   pluginapi.Plugin
	metadata pluginapi.Metadata
}

// Registry has exactly one active plugin per vendor in V1.
type Registry struct {
	mu         sync.RWMutex
	runtimeSDK pluginapi.Version
	entries    map[pluginapi.Vendor]entry
}

func New(runtimeSDK pluginapi.Version) (*Registry, error) {
	if err := runtimeSDK.Validate(); err != nil {
		return nil, fmt.Errorf("validate runtime SDK: %w", err)
	}
	return &Registry{runtimeSDK: runtimeSDK, entries: make(map[pluginapi.Vendor]entry)}, nil
}

func NewCurrent() *Registry {
	registry, err := New(pluginapi.CurrentSDKVersion)
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *Registry) Register(plugin pluginapi.Plugin) error {
	if r == nil {
		return errors.New("plugin registry is nil")
	}
	if pluginapi.IsNilPlugin(plugin) {
		return errors.New("plugin is nil")
	}
	metadata := plugin.Metadata().Clone()
	if metadata.SDKVersion.Validate() == nil && !r.runtimeSDK.CompatibleWith(metadata.SDKVersion) {
		return fmt.Errorf("%w: plugin requires %s, runtime is %s", ErrIncompatibleSDK, metadata.SDKVersion, r.runtimeSDK)
	}
	if err := metadata.Validate(r.runtimeSDK); err != nil {
		return fmt.Errorf("validate plugin metadata: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[metadata.Vendor]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateVendor, metadata.Vendor)
	}
	r.entries[metadata.Vendor] = entry{plugin: plugin, metadata: metadata}
	return nil
}

func (r *Registry) Resolve(vendor pluginapi.Vendor) (pluginapi.Plugin, error) {
	if r == nil {
		return nil, errors.New("plugin registry is nil")
	}
	if err := vendor.Validate(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	installed, ok := r.entries[vendor]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPluginNotFound, vendor)
	}
	return installed.plugin, nil
}

func (r *Registry) Metadata() []pluginapi.Metadata {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	result := make([]pluginapi.Metadata, 0, len(r.entries))
	for _, installed := range r.entries {
		result = append(result, installed.metadata.Clone())
	}
	r.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].Vendor == result[j].Vendor {
			return result[i].Name < result[j].Name
		}
		return result[i].Vendor < result[j].Vendor
	})
	return result
}

// LookupCapability returns a device-specific decision for one declared
// operation. Missing device-specific entries are normalized to UNSUPPORTED.
func (r *Registry) LookupCapability(
	ctx context.Context,
	vendor pluginapi.Vendor,
	info pluginapi.DeviceInfo,
	name pluginapi.OperationName,
) (pluginapi.Capability, error) {
	if ctx == nil {
		return pluginapi.Capability{}, errors.New("context is required")
	}
	if err := pluginapi.ValidateOperationName(name); err != nil {
		return pluginapi.Capability{}, err
	}

	r.mu.RLock()
	installed, ok := r.entries[vendor]
	r.mu.RUnlock()
	if !ok {
		return pluginapi.Capability{}, fmt.Errorf("%w: %s", ErrPluginNotFound, vendor)
	}
	if info.Vendor != vendor {
		return pluginapi.Capability{}, fmt.Errorf("device vendor %s does not match requested vendor %s", info.Vendor, vendor)
	}
	if !installed.metadata.Declares(name) {
		return pluginapi.Capability{}, pluginapi.NewError(pluginapi.ErrorUnsupportedOperation, "operation is not declared by plugin")
	}

	capabilities, err := installed.plugin.Capabilities(ctx, info.Clone())
	if err != nil {
		return pluginapi.Capability{}, err
	}
	if err := capabilities.ValidateAgainst(installed.metadata); err != nil {
		return pluginapi.Capability{}, pluginapi.WrapError(pluginapi.ErrorPlanInvalid, "plugin returned invalid capabilities", err)
	}
	if capability, exists := capabilities.Lookup(name); exists {
		return capability, nil
	}
	return pluginapi.Capability{
		Operation: name,
		Level:     pluginapi.SupportUnsupported,
		Reason:    "operation is not supported for the detected device",
	}, nil
}

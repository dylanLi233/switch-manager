// Package fakeruntime executes only the fake.* protocol used by tests and
// local development. It never emits or interprets real vendor commands.
package fakeruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/acl"
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/domain/route"
	"github.com/dylanLi233/switch-manager/internal/domain/switchinterface"
	"github.com/dylanLi233/switch-manager/internal/domain/telemetry"
	"github.com/dylanLi233/switch-manager/internal/domain/vlan"
	"github.com/dylanLi233/switch-manager/internal/inventorysvc"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

type Factory struct {
	mu            sync.RWMutex
	devices       map[string]map[int]vlan.VLAN
	interfaces    map[string]map[string]switchinterface.Interface
	routes        map[string]map[string]route.StaticRoute
	routeCounters map[string]uint64
	acls          map[string]map[string]acl.ACL
	aclCounters   map[string]uint64
	macEntries    map[string][]telemetry.MACEntry
	arpEntries    map[string][]telemetry.ARPEntry
	statuses      map[string]telemetry.DeviceStatus
}

func New() *Factory {
	return &Factory{
		devices: make(map[string]map[int]vlan.VLAN), interfaces: make(map[string]map[string]switchinterface.Interface),
		routes: make(map[string]map[string]route.StaticRoute), routeCounters: make(map[string]uint64),
		acls: make(map[string]map[string]acl.ACL), aclCounters: make(map[string]uint64),
		macEntries: make(map[string][]telemetry.MACEntry), arpEntries: make(map[string][]telemetry.ARPEntry),
		statuses: make(map[string]telemetry.DeviceStatus),
	}
}

func (f *Factory) Open(ctx context.Context, managed device.Device) (operationsvc.Session, error) {
	if f == nil { return nil, errors.New("fake runtime is nil") }
	if ctx == nil { return nil, errors.New("context is required") }
	if err := ctx.Err(); err != nil { return nil, err }
	if err := managed.Validate(); err != nil { return nil, apperror.Wrap(apperror.CodeValidationError, "", err) }
	f.mu.Lock()
	if _, exists := f.devices[managed.ID]; !exists { f.devices[managed.ID] = make(map[int]vlan.VLAN) }
	if _, exists := f.interfaces[managed.ID]; !exists { f.interfaces[managed.ID] = defaultInterfaces() }
	if _, exists := f.routes[managed.ID]; !exists { f.routes[managed.ID] = make(map[string]route.StaticRoute) }
	if _, exists := f.acls[managed.ID]; !exists { f.acls[managed.ID] = make(map[string]acl.ACL) }
	f.ensureTelemetryLocked(managed.ID)
	f.mu.Unlock()
	return &session{factory: f, deviceID: managed.ID, vendor: managed.Vendor}, nil
}

func defaultInterfaces() map[string]switchinterface.Interface {
	return map[string]switchinterface.Interface{
		"FakeEthernet1/0/1": {Name: "FakeEthernet1/0/1", Description: "fake access port", AdminState: switchinterface.AdminEnabled, OperState: switchinterface.OperUp, Mode: switchinterface.ModeAccess, AccessVLAN: 1},
		"FakeEthernet1/0/2": {Name: "FakeEthernet1/0/2", Description: "fake trunk port", AdminState: switchinterface.AdminEnabled, OperState: switchinterface.OperUp, Mode: switchinterface.ModeTrunk, NativeVLAN: 1, AllowedVLANs: []int{1}},
	}
}

func (f *Factory) Detect(ctx context.Context, managed device.Device, _ inventorysvc.AuthenticationMaterial) (inventorysvc.DetectionResult, error) {
	if ctx == nil { return inventorysvc.DetectionResult{}, errors.New("context is required") }
	if err := ctx.Err(); err != nil { return inventorysvc.DetectionResult{}, err }
	if err := managed.Vendor.Validate(); err != nil { return inventorysvc.DetectionResult{}, apperror.Wrap(apperror.CodeValidationError, "", err) }
	return inventorysvc.DetectionResult{
		Vendor: managed.Vendor, Model: "FAKE-SW", OSVersion: "fake-1.0", EvidenceSummary: "explicit fake runtime fixture",
		Capabilities: []string{
			"vlan.list", "vlan.get", "vlan.create", "vlan.update", "vlan.delete",
			"interface.list", "interface.get", "interface.enable", "interface.disable", "interface.access", "interface.trunk", "interface.vlan.add", "interface.vlan.remove",
			"route.list", "route.get", "route.create", "route.update", "route.delete",
			"acl.list", "acl.get", "acl.create", "acl.update", "acl.delete",
			"mac_table.list", "arp_table.list", "device_status.get", "config.save",
			"command.execute_readonly", "command.execute_config",
		},
	}, nil
}

func (f *Factory) Snapshot(deviceID string) []vlan.VLAN {
	if f == nil { return nil }
	f.mu.RLock(); values := f.devices[deviceID]; result := make([]vlan.VLAN, 0, len(values)); for _, value := range values { result = append(result, value) }; f.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID }); return result
}

func (f *Factory) SnapshotInterfaces(deviceID string) []switchinterface.Interface {
	if f == nil { return nil }
	f.mu.RLock(); values := f.interfaces[deviceID]; result := make([]switchinterface.Interface, 0, len(values)); for _, value := range values { value.AllowedVLANs = append([]int(nil), value.AllowedVLANs...); result = append(result, value) }; f.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name }); return result
}

type session struct { factory *Factory; deviceID string; vendor device.Vendor; mu sync.Mutex; closed bool }

func (s *session) Close() error { if s == nil { return nil }; s.mu.Lock(); s.closed = true; s.mu.Unlock(); return nil }

func (s *session) Execute(ctx context.Context, command pluginapi.PlannedCommand) (pluginapi.CommandOutput, error) {
	started := time.Now()
	if s == nil || s.factory == nil { return pluginapi.CommandOutput{}, errors.New("fake session is not initialized") }
	if ctx == nil { return pluginapi.CommandOutput{}, errors.New("context is required") }
	if err := ctx.Err(); err != nil { return pluginapi.CommandOutput{}, err }
	s.mu.Lock(); if s.closed { s.mu.Unlock(); return pluginapi.CommandOutput{}, apperror.New(apperror.CodeCommandRejected, "") }; s.mu.Unlock()
	if strings.ContainsAny(command.Text, "\r\n\x00") { return pluginapi.CommandOutput{}, apperror.New(apperror.CodeCommandRejected, "") }
	output, err := s.execute(command.Text)
	return pluginapi.CommandOutput{Output: output, Duration: time.Since(started)}, err
}

func (s *session) execute(text string) (string, error) {
	switch {
	case text == "fake.detect": return "vendor=" + string(s.vendor) + ";model=FAKE-SW;os=fake-1.0;prompt=fake", nil
	case text == "fake.config.save": return `{"saved":true}`, nil
	case text == "fake.vlan.list": return marshal(map[string]any{"vlans": s.factory.Snapshot(s.deviceID)})
	case strings.HasPrefix(text, "fake.vlan.get "):
		payload, err := decodePayload(strings.TrimPrefix(text, "fake.vlan.get ")); if err != nil { return "", err }
		s.factory.mu.RLock(); value, exists := s.factory.devices[s.deviceID][payload.VLANID]; s.factory.mu.RUnlock(); if !exists { return "", apperror.New(apperror.CodeResourceNotFound, "") }; return marshal(map[string]any{"vlan": value})
	case strings.HasPrefix(text, "fake.vlan.create "):
		payload, err := decodePayload(strings.TrimPrefix(text, "fake.vlan.create ")); if err != nil { return "", err }; value := vlan.VLAN{ID: payload.VLANID, Name: payload.Name}; if err := value.Validate(); err != nil { return "", apperror.Wrap(apperror.CodeCommandRejected, "", err) }
		s.factory.mu.Lock(); values := s.factory.devices[s.deviceID]; if _, exists := values[value.ID]; exists { s.factory.mu.Unlock(); return "", apperror.New(apperror.CodeStateConflict, "") }; values[value.ID] = value; s.factory.mu.Unlock(); return marshal(map[string]any{"vlan": value})
	case strings.HasPrefix(text, "fake.vlan.update "):
		payload, err := decodePayload(strings.TrimPrefix(text, "fake.vlan.update ")); if err != nil { return "", err }; value := vlan.VLAN{ID: payload.VLANID, Name: payload.Name}; if err := value.Validate(); err != nil { return "", apperror.Wrap(apperror.CodeCommandRejected, "", err) }
		s.factory.mu.Lock(); values := s.factory.devices[s.deviceID]; if _, exists := values[value.ID]; !exists { s.factory.mu.Unlock(); return "", apperror.New(apperror.CodeResourceNotFound, "") }; values[value.ID] = value; s.factory.mu.Unlock(); return marshal(map[string]any{"vlan": value})
	case strings.HasPrefix(text, "fake.vlan.delete "):
		payload, err := decodePayload(strings.TrimPrefix(text, "fake.vlan.delete ")); if err != nil { return "", err }; s.factory.mu.Lock(); values := s.factory.devices[s.deviceID]; if _, exists := values[payload.VLANID]; !exists { s.factory.mu.Unlock(); return "", apperror.New(apperror.CodeResourceNotFound, "") }; delete(values, payload.VLANID); s.factory.mu.Unlock(); return marshal(map[string]any{"deleted": true, "vlan_id": payload.VLANID})
	case text == "fake.interface.list" || strings.HasPrefix(text, "fake.interface."): return s.executeInterface(text)
	case text == "fake.route.list" || strings.HasPrefix(text, "fake.route.") || text == "fake.acl.list" || strings.HasPrefix(text, "fake.acl."): return s.executeRouteACL(text)
	case strings.HasPrefix(text, "fake.mac_table.list ") || strings.HasPrefix(text, "fake.arp_table.list ") || text == "fake.device_status.get": return s.executeTelemetry(text)
	case strings.HasPrefix(text, "fake.echo.query ") || strings.HasPrefix(text, "fake.echo.config "):
		_, quoted, ok := strings.Cut(text, " "); if !ok { return "", apperror.New(apperror.CodeCommandRejected, "") }; value, err := strconv.Unquote(quoted); if err != nil { return "", apperror.Wrap(apperror.CodeCommandRejected, "", err) }; return value, nil
	case strings.HasPrefix(text, "fake.show "):
		value := strings.TrimPrefix(text, "fake.show ")
		switch value {
		case "secret": return "token=secret-token", nil
		case "large": return strings.Repeat("x", 5000), nil
		default: return "readonly:" + value, nil
		}
	case strings.HasPrefix(text, "fake.set "):
		return "configured:" + strings.TrimPrefix(text, "fake.set "), nil
	default: return "", apperror.New(apperror.CodeCommandRejected, "")
	}
}

type vlanPayload struct { VLANID int `json:"vlan_id"`; Name string `json:"name,omitempty"` }

func decodePayload(encoded string) (vlanPayload, error) {
	var payload vlanPayload; decoder := json.NewDecoder(strings.NewReader(encoded)); decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil { return payload, apperror.Wrap(apperror.CodeCommandRejected, "", err) }
	var extra any; if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) { return payload, apperror.New(apperror.CodeCommandRejected, "") }
	if err := vlan.ValidateID(payload.VLANID); err != nil { return payload, apperror.Wrap(apperror.CodeCommandRejected, "", err) }
	if _, err := vlan.NormalizeName(payload.Name, false); err != nil { return payload, apperror.Wrap(apperror.CodeCommandRejected, "", err) }
	return payload, nil
}

func marshal(value any) (string, error) { encoded, err := json.Marshal(value); if err != nil { return "", apperror.Wrap(apperror.CodeInternalError, "", err) }; return string(encoded), nil }

var _ operationsvc.SessionFactory = (*Factory)(nil)
var _ inventorysvc.IdentityDetector = (*Factory)(nil)

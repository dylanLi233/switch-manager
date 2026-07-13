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
	"github.com/dylanLi233/switch-manager/internal/domain/device"
	"github.com/dylanLi233/switch-manager/internal/domain/vlan"
	"github.com/dylanLi233/switch-manager/internal/inventorysvc"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

type Factory struct {
	mu      sync.RWMutex
	devices map[string]map[int]vlan.VLAN
}

func New() *Factory { return &Factory{devices: make(map[string]map[int]vlan.VLAN)} }

func (f *Factory) Open(ctx context.Context, managed device.Device) (operationsvc.Session, error) {
	if f == nil {
		return nil, errors.New("fake runtime is nil")
	}
	if ctx == nil {
		return nil, errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := managed.Validate(); err != nil {
		return nil, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	f.mu.Lock()
	if _, exists := f.devices[managed.ID]; !exists {
		f.devices[managed.ID] = make(map[int]vlan.VLAN)
	}
	f.mu.Unlock()
	return &session{factory: f, deviceID: managed.ID, vendor: managed.Vendor}, nil
}

// Detect provides a deterministic identity fixture when the fake runtime is
// explicitly enabled. Authentication material is ignored and never retained.
func (f *Factory) Detect(ctx context.Context, managed device.Device, _ inventorysvc.AuthenticationMaterial) (inventorysvc.DetectionResult, error) {
	if ctx == nil {
		return inventorysvc.DetectionResult{}, errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return inventorysvc.DetectionResult{}, err
	}
	if err := managed.Vendor.Validate(); err != nil {
		return inventorysvc.DetectionResult{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	return inventorysvc.DetectionResult{
		Vendor: managed.Vendor, Model: "FAKE-SW", OSVersion: "fake-1.0",
		EvidenceSummary: "explicit fake runtime fixture",
		Capabilities: []string{"vlan.list", "vlan.get", "vlan.create", "vlan.update", "vlan.delete", "config.save"},
	}, nil
}

func (f *Factory) Snapshot(deviceID string) []vlan.VLAN {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	values := f.devices[deviceID]
	result := make([]vlan.VLAN, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	f.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

type session struct {
	factory  *Factory
	deviceID string
	vendor   device.Vendor
	mu       sync.Mutex
	closed   bool
}

func (s *session) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func (s *session) Execute(ctx context.Context, command pluginapi.PlannedCommand) (pluginapi.CommandOutput, error) {
	started := time.Now()
	if s == nil || s.factory == nil {
		return pluginapi.CommandOutput{}, errors.New("fake session is not initialized")
	}
	if ctx == nil {
		return pluginapi.CommandOutput{}, errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return pluginapi.CommandOutput{}, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return pluginapi.CommandOutput{}, apperror.New(apperror.CodeCommandRejected, "")
	}
	s.mu.Unlock()
	if strings.ContainsAny(command.Text, "\r\n\x00") {
		return pluginapi.CommandOutput{}, apperror.New(apperror.CodeCommandRejected, "")
	}

	output, err := s.execute(command.Text)
	return pluginapi.CommandOutput{Output: output, Duration: time.Since(started)}, err
}

func (s *session) execute(text string) (string, error) {
	switch {
	case text == "fake.detect":
		return "vendor=" + string(s.vendor) + ";model=FAKE-SW;os=fake-1.0;prompt=fake", nil
	case text == "fake.config.save":
		return `{"saved":true}`, nil
	case text == "fake.vlan.list":
		return marshal(map[string]any{"vlans": s.factory.Snapshot(s.deviceID)})
	case strings.HasPrefix(text, "fake.vlan.get "):
		payload, err := decodePayload(strings.TrimPrefix(text, "fake.vlan.get "))
		if err != nil {
			return "", err
		}
		s.factory.mu.RLock()
		value, exists := s.factory.devices[s.deviceID][payload.VLANID]
		s.factory.mu.RUnlock()
		if !exists {
			return "", apperror.New(apperror.CodeResourceNotFound, "")
		}
		return marshal(map[string]any{"vlan": value})
	case strings.HasPrefix(text, "fake.vlan.create "):
		payload, err := decodePayload(strings.TrimPrefix(text, "fake.vlan.create "))
		if err != nil {
			return "", err
		}
		value := vlan.VLAN{ID: payload.VLANID, Name: payload.Name}
		if err := value.Validate(); err != nil {
			return "", apperror.Wrap(apperror.CodeCommandRejected, "", err)
		}
		s.factory.mu.Lock()
		values := s.factory.devices[s.deviceID]
		if _, exists := values[value.ID]; exists {
			s.factory.mu.Unlock()
			return "", apperror.New(apperror.CodeStateConflict, "")
		}
		values[value.ID] = value
		s.factory.mu.Unlock()
		return marshal(map[string]any{"vlan": value})
	case strings.HasPrefix(text, "fake.vlan.update "):
		payload, err := decodePayload(strings.TrimPrefix(text, "fake.vlan.update "))
		if err != nil {
			return "", err
		}
		value := vlan.VLAN{ID: payload.VLANID, Name: payload.Name}
		if err := value.Validate(); err != nil {
			return "", apperror.Wrap(apperror.CodeCommandRejected, "", err)
		}
		s.factory.mu.Lock()
		values := s.factory.devices[s.deviceID]
		if _, exists := values[value.ID]; !exists {
			s.factory.mu.Unlock()
			return "", apperror.New(apperror.CodeResourceNotFound, "")
		}
		values[value.ID] = value
		s.factory.mu.Unlock()
		return marshal(map[string]any{"vlan": value})
	case strings.HasPrefix(text, "fake.vlan.delete "):
		payload, err := decodePayload(strings.TrimPrefix(text, "fake.vlan.delete "))
		if err != nil {
			return "", err
		}
		s.factory.mu.Lock()
		values := s.factory.devices[s.deviceID]
		if _, exists := values[payload.VLANID]; !exists {
			s.factory.mu.Unlock()
			return "", apperror.New(apperror.CodeResourceNotFound, "")
		}
		delete(values, payload.VLANID)
		s.factory.mu.Unlock()
		return marshal(map[string]any{"deleted": true, "vlan_id": payload.VLANID})
	case strings.HasPrefix(text, "fake.echo.query ") || strings.HasPrefix(text, "fake.echo.config "):
		_, quoted, ok := strings.Cut(text, " ")
		if !ok {
			return "", apperror.New(apperror.CodeCommandRejected, "")
		}
		value, err := strconv.Unquote(quoted)
		if err != nil {
			return "", apperror.Wrap(apperror.CodeCommandRejected, "", err)
		}
		return value, nil
	default:
		return "", apperror.New(apperror.CodeCommandRejected, "")
	}
}

type vlanPayload struct {
	VLANID int    `json:"vlan_id"`
	Name   string `json:"name,omitempty"`
}

func decodePayload(encoded string) (vlanPayload, error) {
	var payload vlanPayload
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return payload, apperror.Wrap(apperror.CodeCommandRejected, "", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return payload, apperror.New(apperror.CodeCommandRejected, "")
	}
	if err := vlan.ValidateID(payload.VLANID); err != nil {
		return payload, apperror.Wrap(apperror.CodeCommandRejected, "", err)
	}
	if _, err := vlan.NormalizeName(payload.Name, false); err != nil {
		return payload, apperror.Wrap(apperror.CodeCommandRejected, "", err)
	}
	return payload, nil
}

func marshal(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	return string(encoded), nil
}

var _ operationsvc.SessionFactory = (*Factory)(nil)
var _ inventorysvc.IdentityDetector = (*Factory)(nil)

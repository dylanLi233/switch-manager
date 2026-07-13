package fakeruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/acl"
	"github.com/dylanLi233/switch-manager/internal/domain/route"
)

type runtimeRoutePayload struct {
	RouteID string      `json:"route_id,omitempty"`
	Route   *route.Spec `json:"route,omitempty"`
}

type runtimeACLPayload struct {
	ACLID string    `json:"acl_id,omitempty"`
	ACL   *acl.Spec `json:"acl,omitempty"`
}

func (f *Factory) SnapshotRoutes(deviceID string) []route.StaticRoute {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	result := make([]route.StaticRoute, 0, len(f.routes[deviceID]))
	for _, value := range f.routes[deviceID] {
		result = append(result, value)
	}
	f.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].RouteID < result[j].RouteID })
	return result
}

func (f *Factory) SnapshotACLs(deviceID string) []acl.ACL {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	result := make([]acl.ACL, 0, len(f.acls[deviceID]))
	for _, value := range f.acls[deviceID] {
		result = append(result, cloneACL(value))
	}
	f.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ACLID < result[j].ACLID })
	return result
}

func (s *session) executeRouteACL(text string) (string, error) {
	switch {
	case text == "fake.route.list":
		return marshal(map[string]any{"routes": s.factory.SnapshotRoutes(s.deviceID)})
	case strings.HasPrefix(text, "fake.route.get "):
		payload, err := decodeRuntimeRoutePayload(strings.TrimPrefix(text, "fake.route.get "))
		if err != nil || route.ValidateID(payload.RouteID) != nil || payload.Route != nil {
			return "", commandRejected(err)
		}
		s.factory.mu.RLock()
		value, exists := s.factory.routes[s.deviceID][payload.RouteID]
		s.factory.mu.RUnlock()
		if !exists {
			return "", apperror.New(apperror.CodeResourceNotFound, "")
		}
		return marshal(map[string]any{"route": value})
	case strings.HasPrefix(text, "fake.route.create "):
		payload, err := decodeRuntimeRoutePayload(strings.TrimPrefix(text, "fake.route.create "))
		if err != nil || payload.RouteID != "" || payload.Route == nil {
			return "", commandRejected(err)
		}
		spec, err := route.NormalizeSpec(*payload.Route)
		if err != nil {
			return "", commandRejected(err)
		}
		s.factory.mu.Lock()
		if spec.OutgoingInterface != "" {
			if _, exists := s.factory.interfaces[s.deviceID][spec.OutgoingInterface]; !exists {
				s.factory.mu.Unlock()
				return "", apperror.New(apperror.CodeResourceNotFound, "")
			}
		}
		for _, existing := range s.factory.routes[s.deviceID] {
			if existing.Spec == spec {
				s.factory.mu.Unlock()
				return "", apperror.New(apperror.CodeStateConflict, "")
			}
		}
		s.factory.routeCounters[s.deviceID]++
		id := fmt.Sprintf("route-%06d", s.factory.routeCounters[s.deviceID])
		value := route.StaticRoute{RouteID: id, Spec: spec}
		s.factory.routes[s.deviceID][id] = value
		s.factory.mu.Unlock()
		return marshal(map[string]any{"route": value})
	case strings.HasPrefix(text, "fake.route.update "):
		payload, err := decodeRuntimeRoutePayload(strings.TrimPrefix(text, "fake.route.update "))
		if err != nil || route.ValidateID(payload.RouteID) != nil || payload.Route == nil {
			return "", commandRejected(err)
		}
		spec, err := route.NormalizeSpec(*payload.Route)
		if err != nil {
			return "", commandRejected(err)
		}
		s.factory.mu.Lock()
		if _, exists := s.factory.routes[s.deviceID][payload.RouteID]; !exists {
			s.factory.mu.Unlock()
			return "", apperror.New(apperror.CodeResourceNotFound, "")
		}
		if spec.OutgoingInterface != "" {
			if _, exists := s.factory.interfaces[s.deviceID][spec.OutgoingInterface]; !exists {
				s.factory.mu.Unlock()
				return "", apperror.New(apperror.CodeResourceNotFound, "")
			}
		}
		for id, existing := range s.factory.routes[s.deviceID] {
			if id != payload.RouteID && existing.Spec == spec {
				s.factory.mu.Unlock()
				return "", apperror.New(apperror.CodeStateConflict, "")
			}
		}
		value := route.StaticRoute{RouteID: payload.RouteID, Spec: spec}
		s.factory.routes[s.deviceID][payload.RouteID] = value
		s.factory.mu.Unlock()
		return marshal(map[string]any{"route": value})
	case strings.HasPrefix(text, "fake.route.delete "):
		payload, err := decodeRuntimeRoutePayload(strings.TrimPrefix(text, "fake.route.delete "))
		if err != nil || route.ValidateID(payload.RouteID) != nil || payload.Route != nil {
			return "", commandRejected(err)
		}
		s.factory.mu.Lock()
		if _, exists := s.factory.routes[s.deviceID][payload.RouteID]; !exists {
			s.factory.mu.Unlock()
			return "", apperror.New(apperror.CodeResourceNotFound, "")
		}
		delete(s.factory.routes[s.deviceID], payload.RouteID)
		s.factory.mu.Unlock()
		return marshal(map[string]any{"deleted": true, "route_id": payload.RouteID})
	case text == "fake.acl.list":
		return marshal(map[string]any{"schema_version": acl.ExperimentalSchemaVersion, "acls": s.factory.SnapshotACLs(s.deviceID)})
	case strings.HasPrefix(text, "fake.acl.get "):
		payload, err := decodeRuntimeACLPayload(strings.TrimPrefix(text, "fake.acl.get "))
		if err != nil || acl.ValidateID(payload.ACLID) != nil || payload.ACL != nil {
			return "", commandRejected(err)
		}
		s.factory.mu.RLock()
		value, exists := s.factory.acls[s.deviceID][payload.ACLID]
		s.factory.mu.RUnlock()
		if !exists {
			return "", apperror.New(apperror.CodeResourceNotFound, "")
		}
		return marshal(map[string]any{"schema_version": acl.ExperimentalSchemaVersion, "acl": cloneACL(value)})
	case strings.HasPrefix(text, "fake.acl.create "):
		payload, err := decodeRuntimeACLPayload(strings.TrimPrefix(text, "fake.acl.create "))
		if err != nil || payload.ACLID != "" || payload.ACL == nil {
			return "", commandRejected(err)
		}
		spec, err := acl.NormalizeSpec(*payload.ACL)
		if err != nil {
			return "", commandRejected(err)
		}
		s.factory.mu.Lock()
		for _, existing := range s.factory.acls[s.deviceID] {
			if existing.Name == spec.Name {
				s.factory.mu.Unlock()
				return "", apperror.New(apperror.CodeStateConflict, "")
			}
		}
		s.factory.aclCounters[s.deviceID]++
		id := fmt.Sprintf("acl-%06d", s.factory.aclCounters[s.deviceID])
		value := acl.ACL{ACLID: id, Spec: spec}
		s.factory.acls[s.deviceID][id] = cloneACL(value)
		s.factory.mu.Unlock()
		return marshal(map[string]any{"schema_version": acl.ExperimentalSchemaVersion, "acl": value})
	case strings.HasPrefix(text, "fake.acl.update "):
		payload, err := decodeRuntimeACLPayload(strings.TrimPrefix(text, "fake.acl.update "))
		if err != nil || acl.ValidateID(payload.ACLID) != nil || payload.ACL == nil {
			return "", commandRejected(err)
		}
		spec, err := acl.NormalizeSpec(*payload.ACL)
		if err != nil {
			return "", commandRejected(err)
		}
		s.factory.mu.Lock()
		if _, exists := s.factory.acls[s.deviceID][payload.ACLID]; !exists {
			s.factory.mu.Unlock()
			return "", apperror.New(apperror.CodeResourceNotFound, "")
		}
		for id, existing := range s.factory.acls[s.deviceID] {
			if id != payload.ACLID && existing.Name == spec.Name {
				s.factory.mu.Unlock()
				return "", apperror.New(apperror.CodeStateConflict, "")
			}
		}
		value := acl.ACL{ACLID: payload.ACLID, Spec: spec}
		s.factory.acls[s.deviceID][payload.ACLID] = cloneACL(value)
		s.factory.mu.Unlock()
		return marshal(map[string]any{"schema_version": acl.ExperimentalSchemaVersion, "acl": value})
	case strings.HasPrefix(text, "fake.acl.delete "):
		payload, err := decodeRuntimeACLPayload(strings.TrimPrefix(text, "fake.acl.delete "))
		if err != nil || acl.ValidateID(payload.ACLID) != nil || payload.ACL != nil {
			return "", commandRejected(err)
		}
		s.factory.mu.Lock()
		if _, exists := s.factory.acls[s.deviceID][payload.ACLID]; !exists {
			s.factory.mu.Unlock()
			return "", apperror.New(apperror.CodeResourceNotFound, "")
		}
		delete(s.factory.acls[s.deviceID], payload.ACLID)
		s.factory.mu.Unlock()
		return marshal(map[string]any{"schema_version": acl.ExperimentalSchemaVersion, "deleted": true, "acl_id": payload.ACLID})
	default:
		return "", apperror.New(apperror.CodeCommandRejected, "")
	}
}

func decodeRuntimeRoutePayload(encoded string) (runtimeRoutePayload, error) {
	var payload runtimeRoutePayload
	return payload, decodeRuntimeObject(encoded, &payload)
}

func decodeRuntimeACLPayload(encoded string) (runtimeACLPayload, error) {
	var payload runtimeACLPayload
	return payload, decodeRuntimeObject(encoded, &payload)
}

func decodeRuntimeObject(encoded string, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("command payload must contain one JSON object")
	}
	return nil
}

func commandRejected(err error) error {
	if err == nil {
		err = errors.New("command payload is invalid")
	}
	return apperror.Wrap(apperror.CodeCommandRejected, "", err)
}

func cloneACL(value acl.ACL) acl.ACL {
	value.Rules = append([]acl.Rule(nil), value.Rules...)
	for index := range value.Rules {
		value.Rules[index].SourcePorts = append([]acl.PortRange(nil), value.Rules[index].SourcePorts...)
		value.Rules[index].DestinationPorts = append([]acl.PortRange(nil), value.Rules[index].DestinationPorts...)
	}
	return value
}

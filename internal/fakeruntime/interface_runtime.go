package fakeruntime

import (
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/switchinterface"
	"github.com/dylanLi233/switch-manager/internal/domain/vlan"
)

type interfacePayload struct {
	Name         string `json:"interface_name"`
	VLANID       int    `json:"vlan_id,omitempty"`
	AllowedVLANs []int  `json:"allowed_vlans,omitempty"`
	NativeVLAN   int    `json:"native_vlan,omitempty"`
}

func (s *session) executeInterface(text string) (string, error) {
	if text == "fake.interface.list" {
		return marshal(map[string]any{"interfaces": s.factory.SnapshotInterfaces(s.deviceID)})
	}
	operation, encoded, ok := strings.Cut(text, " ")
	if !ok {
		return "", apperror.New(apperror.CodeCommandRejected, "")
	}
	payload, err := decodeInterfacePayload(encoded)
	if err != nil {
		return "", err
	}

	s.factory.mu.Lock()
	defer s.factory.mu.Unlock()
	interfaces := s.factory.interfaces[s.deviceID]
	current, exists := interfaces[payload.Name]
	if !exists {
		return "", apperror.New(apperror.CodeResourceNotFound, "")
	}
	current.AllowedVLANs = append([]int(nil), current.AllowedVLANs...)

	switch operation {
	case "fake.interface.get":
	case "fake.interface.enable":
		current.AdminState, current.OperState = switchinterface.AdminEnabled, switchinterface.OperUp
	case "fake.interface.disable":
		current.AdminState, current.OperState = switchinterface.AdminDisabled, switchinterface.OperDown
	case "fake.interface.access":
		if err := vlan.ValidateID(payload.VLANID); err != nil {
			return "", apperror.Wrap(apperror.CodeCommandRejected, "", err)
		}
		current.Mode = switchinterface.ModeAccess
		current.AccessVLAN = payload.VLANID
		current.NativeVLAN = 0
		current.AllowedVLANs = nil
	case "fake.interface.trunk":
		allowed, err := switchinterface.NormalizeVLANs(payload.AllowedVLANs, true)
		if err != nil {
			return "", apperror.Wrap(apperror.CodeCommandRejected, "", err)
		}
		if payload.NativeVLAN != 0 && !containsInterfaceVLAN(allowed, payload.NativeVLAN) {
			return "", apperror.New(apperror.CodeCommandRejected, "")
		}
		current.Mode = switchinterface.ModeTrunk
		current.AccessVLAN = 0
		current.NativeVLAN = payload.NativeVLAN
		current.AllowedVLANs = allowed
	case "fake.interface.vlan.add":
		if current.Mode != switchinterface.ModeTrunk {
			return "", apperror.New(apperror.CodeStateConflict, "")
		}
		if err := vlan.ValidateID(payload.VLANID); err != nil {
			return "", apperror.Wrap(apperror.CodeCommandRejected, "", err)
		}
		if containsInterfaceVLAN(current.AllowedVLANs, payload.VLANID) {
			return "", apperror.New(apperror.CodeStateConflict, "")
		}
		current.AllowedVLANs = append(current.AllowedVLANs, payload.VLANID)
		sort.Ints(current.AllowedVLANs)
	case "fake.interface.vlan.remove":
		if current.Mode != switchinterface.ModeTrunk {
			return "", apperror.New(apperror.CodeStateConflict, "")
		}
		if payload.VLANID == current.NativeVLAN {
			return "", apperror.New(apperror.CodeStateConflict, "")
		}
		index := -1
		for candidate, id := range current.AllowedVLANs {
			if id == payload.VLANID {
				index = candidate
				break
			}
		}
		if index < 0 {
			return "", apperror.New(apperror.CodeResourceNotFound, "")
		}
		current.AllowedVLANs = append(current.AllowedVLANs[:index], current.AllowedVLANs[index+1:]...)
		if len(current.AllowedVLANs) == 0 {
			return "", apperror.New(apperror.CodeStateConflict, "")
		}
	default:
		return "", apperror.New(apperror.CodeCommandRejected, "")
	}
	if err := current.Validate(); err != nil {
		return "", apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	interfaces[payload.Name] = current
	return marshal(map[string]any{"interface": current})
}

func decodeInterfacePayload(encoded string) (interfacePayload, error) {
	var payload interfacePayload
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return payload, apperror.Wrap(apperror.CodeCommandRejected, "", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return payload, apperror.New(apperror.CodeCommandRejected, "")
	}
	if err := switchinterface.ValidateNameSafety(payload.Name); err != nil {
		return payload, apperror.Wrap(apperror.CodeCommandRejected, "", err)
	}
	if payload.VLANID != 0 {
		if err := vlan.ValidateID(payload.VLANID); err != nil {
			return payload, apperror.Wrap(apperror.CodeCommandRejected, "", err)
		}
	}
	if _, err := switchinterface.NormalizeVLANs(payload.AllowedVLANs, false); err != nil {
		return payload, apperror.Wrap(apperror.CodeCommandRejected, "", err)
	}
	if payload.NativeVLAN != 0 {
		if err := vlan.ValidateID(payload.NativeVLAN); err != nil {
			return payload, apperror.Wrap(apperror.CodeCommandRejected, "", err)
		}
	}
	return payload, nil
}

func containsInterfaceVLAN(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

package fake

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	"github.com/dylanLi233/switch-manager/internal/domain/switchinterface"
	"github.com/dylanLi233/switch-manager/internal/domain/vlan"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

var fakeInterfaceNamePattern = regexp.MustCompile(`^FakeEthernet[1-9][0-9]*/[0-9]+/[0-9]+$`)

// ValidateInterfaceName owns the Fake fixture syntax. The core deliberately
// does not apply this expression to real vendor plugins.
func (p *Plugin) ValidateInterfaceName(name string) error {
	if err := switchinterface.ValidateNameSafety(name); err != nil {
		return err
	}
	if !fakeInterfaceNamePattern.MatchString(name) {
		return fmt.Errorf("unsupported fake interface name %q", name)
	}
	return nil
}

type interfacePayload struct {
	Name         string `json:"interface_name"`
	VLANID       int    `json:"vlan_id,omitempty"`
	AllowedVLANs []int  `json:"allowed_vlans,omitempty"`
	NativeVLAN   int    `json:"native_vlan,omitempty"`
}

func interfaceCommand(p *Plugin, request pluginapi.PlanRequest) (string, pluginapi.RiskLevel, bool, error) {
	query := request.Operation == pluginapi.OperationInterfaceList || request.Operation == pluginapi.OperationInterfaceGet
	if query && request.Class != pluginapi.ClassQuery {
		return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "interface query requires QUERY class")
	}
	if !query && request.Class != pluginapi.ClassConfig {
		return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "interface configuration requires CONFIG class")
	}
	if request.Operation == pluginapi.OperationInterfaceList {
		if len(request.Parameters) != 0 {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "interface.list accepts no parameters")
		}
		return "fake.interface.list", pluginapi.RiskLow, false, nil
	}
	name, ok := request.Parameters["interface_name"].(string)
	if !ok || p.ValidateInterfaceName(name) != nil {
		return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "interface_name is not valid for the fake plugin")
	}
	payload := interfacePayload{Name: name}
	prefix := ""
	allowedKeys := map[string]struct{}{"interface_name": {}}
	risk, enterConfig := pluginapi.RiskLow, false
	switch request.Operation {
	case pluginapi.OperationInterfaceGet:
		prefix = "fake.interface.get"
	case pluginapi.OperationInterfaceEnable:
		prefix, risk, enterConfig = "fake.interface.enable", pluginapi.RiskMedium, true
	case pluginapi.OperationInterfaceDisable:
		prefix, risk, enterConfig = "fake.interface.disable", pluginapi.RiskMedium, true
	case pluginapi.OperationInterfaceAccess:
		prefix, risk, enterConfig = "fake.interface.access", pluginapi.RiskMedium, true
		allowedKeys["vlan_id"] = struct{}{}
		id, err := integerParameter(request.Parameters["vlan_id"])
		if err != nil || vlan.ValidateID(id) != nil {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "vlan_id must be between 1 and 4094")
		}
		payload.VLANID = id
	case pluginapi.OperationInterfaceTrunk:
		prefix, risk, enterConfig = "fake.interface.trunk", pluginapi.RiskMedium, true
		allowedKeys["allowed_vlans"] = struct{}{}
		allowedKeys["native_vlan"] = struct{}{}
		values, err := integerSliceParameter(request.Parameters["allowed_vlans"])
		if err != nil {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "allowed_vlans must be an integer array")
		}
		values, err = switchinterface.NormalizeVLANs(values, true)
		if err != nil {
			return "", "", false, pluginapi.WrapError(pluginapi.ErrorInvalidRequest, "allowed_vlans are invalid", err)
		}
		payload.AllowedVLANs = values
		if raw, exists := request.Parameters["native_vlan"]; exists && raw != nil {
			native, err := integerParameter(raw)
			if err != nil || vlan.ValidateID(native) != nil || !containsInt(values, native) {
				return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "native_vlan must be included in allowed_vlans")
			}
			payload.NativeVLAN = native
		}
	case pluginapi.OperationInterfaceVLANAdd:
		prefix, risk, enterConfig = "fake.interface.vlan.add", pluginapi.RiskMedium, true
		allowedKeys["vlan_id"] = struct{}{}
		id, err := integerParameter(request.Parameters["vlan_id"])
		if err != nil || vlan.ValidateID(id) != nil {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "vlan_id must be between 1 and 4094")
		}
		payload.VLANID = id
	case pluginapi.OperationInterfaceVLANRemove:
		prefix, risk, enterConfig = "fake.interface.vlan.remove", pluginapi.RiskMedium, true
		allowedKeys["vlan_id"] = struct{}{}
		id, err := integerParameter(request.Parameters["vlan_id"])
		if err != nil || vlan.ValidateID(id) != nil {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "vlan_id must be between 1 and 4094")
		}
		payload.VLANID = id
	default:
		return "", "", false, pluginapi.NewError(pluginapi.ErrorUnsupportedOperation, "interface operation is not declared")
	}
	for key := range request.Parameters {
		if _, exists := allowedKeys[key]; !exists {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "unknown interface parameter")
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", "", false, pluginapi.WrapError(pluginapi.ErrorPlanInvalid, "encode fake interface command", err)
	}
	return prefix + " " + string(encoded), risk, enterConfig, nil
}

func integerSliceParameter(value any) ([]int, error) {
	var result []int
	switch typed := value.(type) {
	case []int:
		result = append([]int(nil), typed...)
	case []any:
		result = make([]int, len(typed))
		for index, item := range typed {
			parsed, err := integerParameter(item)
			if err != nil {
				return nil, err
			}
			result[index] = parsed
		}
	default:
		return nil, fmt.Errorf("unsupported integer slice type %T", value)
	}
	return result, nil
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func isInterfaceOperation(name pluginapi.OperationName) bool {
	switch name {
	case pluginapi.OperationInterfaceList, pluginapi.OperationInterfaceGet,
		pluginapi.OperationInterfaceEnable, pluginapi.OperationInterfaceDisable,
		pluginapi.OperationInterfaceAccess, pluginapi.OperationInterfaceTrunk,
		pluginapi.OperationInterfaceVLANAdd, pluginapi.OperationInterfaceVLANRemove:
		return true
	default:
		return false
	}
}

func validateInterfaceOutput(operation pluginapi.OperationName, data any) error {
	object, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("result must be an object")
	}
	if operation == pluginapi.OperationInterfaceList {
		items, ok := object["interfaces"].([]any)
		if !ok {
			return fmt.Errorf("interfaces array is required")
		}
		for _, item := range items {
			if err := validateInterfaceObject(item); err != nil {
				return err
			}
		}
		return nil
	}
	return validateInterfaceObject(object["interface"])
}

func validateInterfaceObject(value any) error {
	object, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("interface object is required")
	}
	name, _ := object["name"].(string)
	admin, _ := object["admin_state"].(string)
	oper, _ := object["oper_state"].(string)
	mode, _ := object["mode"].(string)
	view := switchinterface.Interface{Name: name, AdminState: switchinterface.AdminState(admin), OperState: switchinterface.OperState(oper), Mode: switchinterface.Mode(mode)}
	if raw, exists := object["access_vlan"]; exists {
		view.AccessVLAN, _ = integerParameter(raw)
	}
	if raw, exists := object["native_vlan"]; exists {
		view.NativeVLAN, _ = integerParameter(raw)
	}
	if raw, exists := object["allowed_vlans"]; exists {
		view.AllowedVLANs, _ = integerSliceParameter(raw)
		sort.Ints(view.AllowedVLANs)
	}
	return view.Validate()
}

var _ pluginapi.InterfaceNameValidator = (*Plugin)(nil)

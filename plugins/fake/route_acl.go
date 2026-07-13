package fake

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/dylanLi233/switch-manager/internal/domain/acl"
	"github.com/dylanLi233/switch-manager/internal/domain/route"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

var fakeACLNamePattern = regexp.MustCompile(`^FAKE_ACL_[A-Z0-9_]{1,32}$`)

func (p *Plugin) ValidateACLName(name string) error {
	if err := acl.ValidateNameSafety(name); err != nil {
		return err
	}
	if !fakeACLNamePattern.MatchString(name) {
		return fmt.Errorf("unsupported fake ACL name %q", name)
	}
	return nil
}

type routePayload struct {
	RouteID string      `json:"route_id,omitempty"`
	Route   *route.Spec `json:"route,omitempty"`
}

type aclPayload struct {
	ACLID string    `json:"acl_id,omitempty"`
	ACL   *acl.Spec `json:"acl,omitempty"`
}

func routeACLCommand(p *Plugin, request pluginapi.PlanRequest) (string, pluginapi.RiskLevel, bool, error) {
	switch request.Operation {
	case pluginapi.OperationRouteList:
		if request.Class != pluginapi.ClassQuery || len(request.Parameters) != 0 {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "route.list requires QUERY class and no parameters")
		}
		return "fake.route.list", pluginapi.RiskLow, false, nil
	case pluginapi.OperationRouteGet:
		if request.Class != pluginapi.ClassQuery {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "route.get requires QUERY class")
		}
		id, err := strictIdentifierParameter(request.Parameters, "route_id", route.ValidateID)
		if err != nil {
			return "", "", false, err
		}
		return encodedCommand("fake.route.get", routePayload{RouteID: id}, pluginapi.RiskLow, false)
	case pluginapi.OperationRouteCreate:
		if request.Class != pluginapi.ClassConfig || len(request.Parameters) != 1 {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "route.create requires CONFIG class and one route object")
		}
		spec, err := routeSpecParameter(request.Parameters["route"])
		if err != nil {
			return "", "", false, err
		}
		if spec.OutgoingInterface != "" {
			validator, ok := any(p).(pluginapi.InterfaceNameValidator)
			if !ok || validator.ValidateInterfaceName(spec.OutgoingInterface) != nil {
				return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "outgoing_interface is not valid for the fake plugin")
			}
		}
		return encodedCommand("fake.route.create", routePayload{Route: &spec}, pluginapi.RiskMedium, true)
	case pluginapi.OperationRouteUpdate:
		if request.Class != pluginapi.ClassConfig || len(request.Parameters) != 2 {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "route.update requires CONFIG class, route_id, and route")
		}
		id, err := identifierValue(request.Parameters["route_id"], route.ValidateID)
		if err != nil {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "route_id is invalid")
		}
		spec, err := routeSpecParameter(request.Parameters["route"])
		if err != nil {
			return "", "", false, err
		}
		if spec.OutgoingInterface != "" && p.ValidateInterfaceName(spec.OutgoingInterface) != nil {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "outgoing_interface is not valid for the fake plugin")
		}
		return encodedCommand("fake.route.update", routePayload{RouteID: id, Route: &spec}, pluginapi.RiskMedium, true)
	case pluginapi.OperationRouteDelete:
		if request.Class != pluginapi.ClassConfig {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "route.delete requires CONFIG class")
		}
		id, err := strictIdentifierParameter(request.Parameters, "route_id", route.ValidateID)
		if err != nil {
			return "", "", false, err
		}
		return encodedCommand("fake.route.delete", routePayload{RouteID: id}, pluginapi.RiskMedium, true)
	case pluginapi.OperationACLList:
		if request.Class != pluginapi.ClassQuery || len(request.Parameters) != 0 {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "acl.list requires QUERY class and no parameters")
		}
		return "fake.acl.list", pluginapi.RiskLow, false, nil
	case pluginapi.OperationACLGet:
		if request.Class != pluginapi.ClassQuery {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "acl.get requires QUERY class")
		}
		id, err := strictIdentifierParameter(request.Parameters, "acl_id", acl.ValidateID)
		if err != nil {
			return "", "", false, err
		}
		return encodedCommand("fake.acl.get", aclPayload{ACLID: id}, pluginapi.RiskLow, false)
	case pluginapi.OperationACLCreate:
		if request.Class != pluginapi.ClassConfig || len(request.Parameters) != 1 {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "acl.create requires CONFIG class and one acl object")
		}
		spec, err := aclSpecParameter(p, request.Parameters["acl"])
		if err != nil {
			return "", "", false, err
		}
		return encodedCommand("fake.acl.create", aclPayload{ACL: &spec}, pluginapi.RiskMedium, true)
	case pluginapi.OperationACLUpdate:
		if request.Class != pluginapi.ClassConfig || len(request.Parameters) != 2 {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "acl.update requires CONFIG class, acl_id, and acl")
		}
		id, err := identifierValue(request.Parameters["acl_id"], acl.ValidateID)
		if err != nil {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "acl_id is invalid")
		}
		spec, err := aclSpecParameter(p, request.Parameters["acl"])
		if err != nil {
			return "", "", false, err
		}
		return encodedCommand("fake.acl.update", aclPayload{ACLID: id, ACL: &spec}, pluginapi.RiskMedium, true)
	case pluginapi.OperationACLDelete:
		if request.Class != pluginapi.ClassConfig {
			return "", "", false, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "acl.delete requires CONFIG class")
		}
		id, err := strictIdentifierParameter(request.Parameters, "acl_id", acl.ValidateID)
		if err != nil {
			return "", "", false, err
		}
		return encodedCommand("fake.acl.delete", aclPayload{ACLID: id}, pluginapi.RiskMedium, true)
	default:
		return "", "", false, pluginapi.NewError(pluginapi.ErrorUnsupportedOperation, "route or ACL operation is not declared")
	}
}

func encodedCommand(prefix string, payload any, risk pluginapi.RiskLevel, config bool) (string, pluginapi.RiskLevel, bool, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", "", false, pluginapi.WrapError(pluginapi.ErrorPlanInvalid, "encode fake structured command", err)
	}
	return prefix + " " + string(encoded), risk, config, nil
}

func strictIdentifierParameter(parameters map[string]any, key string, validate func(string) error) (string, error) {
	if len(parameters) != 1 {
		return "", pluginapi.NewError(pluginapi.ErrorInvalidRequest, key+" is the only supported parameter")
	}
	value, err := identifierValue(parameters[key], validate)
	if err != nil {
		return "", pluginapi.NewError(pluginapi.ErrorInvalidRequest, key+" is invalid")
	}
	return value, nil
}

func identifierValue(value any, validate func(string) error) (string, error) {
	text, ok := value.(string)
	if !ok || validate(text) != nil {
		return "", errors.New("invalid identifier")
	}
	return text, nil
}

func routeSpecParameter(value any) (route.Spec, error) {
	var spec route.Spec
	if err := decodeStrictValue(value, &spec); err != nil {
		return route.Spec{}, pluginapi.WrapError(pluginapi.ErrorInvalidRequest, "route object is invalid", err)
	}
	normalized, err := route.NormalizeSpec(spec)
	if err != nil {
		return route.Spec{}, pluginapi.WrapError(pluginapi.ErrorInvalidRequest, "route object is invalid", err)
	}
	return normalized, nil
}

func aclSpecParameter(p *Plugin, value any) (acl.Spec, error) {
	var spec acl.Spec
	if err := decodeStrictValue(value, &spec); err != nil {
		return acl.Spec{}, pluginapi.WrapError(pluginapi.ErrorInvalidRequest, "ACL object is invalid", err)
	}
	normalized, err := acl.NormalizeSpec(spec)
	if err != nil {
		return acl.Spec{}, pluginapi.WrapError(pluginapi.ErrorInvalidRequest, "ACL object is invalid", err)
	}
	if p.ValidateACLName(normalized.Name) != nil {
		return acl.Spec{}, pluginapi.NewError(pluginapi.ErrorInvalidRequest, "ACL name is not valid for the fake plugin")
	}
	return normalized, nil
}

func decodeStrictValue(value any, destination any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("value must contain exactly one JSON object")
	}
	return nil
}

func isRouteACLOperation(name pluginapi.OperationName) bool {
	switch name {
	case pluginapi.OperationRouteList, pluginapi.OperationRouteGet, pluginapi.OperationRouteCreate, pluginapi.OperationRouteUpdate, pluginapi.OperationRouteDelete,
		pluginapi.OperationACLList, pluginapi.OperationACLGet, pluginapi.OperationACLCreate, pluginapi.OperationACLUpdate, pluginapi.OperationACLDelete:
		return true
	default:
		return false
	}
}

func validateRouteACLOutput(p *Plugin, operation pluginapi.OperationName, data any) error {
	object, ok := data.(map[string]any)
	if !ok {
		return errors.New("result must be an object")
	}
	switch operation {
	case pluginapi.OperationRouteList:
		items, ok := object["routes"].([]any)
		if !ok {
			return errors.New("routes array is required")
		}
		for _, item := range items {
			if err := validateRouteObject(p, item); err != nil {
				return err
			}
		}
	case pluginapi.OperationRouteGet, pluginapi.OperationRouteCreate, pluginapi.OperationRouteUpdate:
		return validateRouteObject(p, object["route"])
	case pluginapi.OperationRouteDelete:
		if deleted, ok := object["deleted"].(bool); !ok || !deleted {
			return errors.New("deleted=true is required")
		}
		id, _ := object["route_id"].(string)
		return route.ValidateID(id)
	case pluginapi.OperationACLList:
		if object["schema_version"] != acl.ExperimentalSchemaVersion {
			return errors.New("experimental ACL schema_version is required")
		}
		items, ok := object["acls"].([]any)
		if !ok {
			return errors.New("acls array is required")
		}
		for _, item := range items {
			if err := validateACLObject(p, item); err != nil {
				return err
			}
		}
	case pluginapi.OperationACLGet, pluginapi.OperationACLCreate, pluginapi.OperationACLUpdate:
		if object["schema_version"] != acl.ExperimentalSchemaVersion {
			return errors.New("experimental ACL schema_version is required")
		}
		return validateACLObject(p, object["acl"])
	case pluginapi.OperationACLDelete:
		if object["schema_version"] != acl.ExperimentalSchemaVersion {
			return errors.New("experimental ACL schema_version is required")
		}
		if deleted, ok := object["deleted"].(bool); !ok || !deleted {
			return errors.New("deleted=true is required")
		}
		id, _ := object["acl_id"].(string)
		return acl.ValidateID(id)
	}
	return nil
}

func validateRouteObject(p *Plugin, value any) error {
	var item route.StaticRoute
	if err := decodeStrictValue(value, &item); err != nil {
		return err
	}
	if err := item.Validate(); err != nil {
		return err
	}
	if item.OutgoingInterface != "" && p.ValidateInterfaceName(item.OutgoingInterface) != nil {
		return errors.New("route output contains an unsupported fake interface")
	}
	return nil
}

func validateACLObject(p *Plugin, value any) error {
	var item acl.ACL
	if err := decodeStrictValue(value, &item); err != nil {
		return err
	}
	if err := item.Validate(); err != nil {
		return err
	}
	if p.ValidateACLName(item.Name) != nil {
		return errors.New("ACL output contains an unsupported fake ACL name")
	}
	return nil
}

var _ pluginapi.ACLNameValidator = (*Plugin)(nil)

package httpserver

import (
	"errors"
	"net/http"
	"strings"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/switchinterface"
	"github.com/dylanLi233/switch-manager/internal/domain/vlan"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

type InterfaceHandlers struct{ operations OperationSubmitter }

func NewInterfaceHandlers(operations OperationSubmitter) (*InterfaceHandlers, error) {
	if operations == nil {
		return nil, errors.New("operation service is required")
	}
	return &InterfaceHandlers{operations: operations}, nil
}

func (h *InterfaceHandlers) Register(mux *http.ServeMux, authenticator Authenticator) {
	if h == nil || h.operations == nil || mux == nil || authenticator == nil {
		return
	}
	register := func(pattern string, permission auth.Permission, handler ErrorHandlerFunc) {
		mux.Handle(pattern, AuthenticationMiddleware(authenticator)(RequirePermission(permission, switchScope)(AdaptErrorHandler(handler))))
	}
	register("GET /api/v1/switches/{switchID}/interfaces", auth.PermissionOperationQuery, h.list)
	register("GET /api/v1/switches/{switchID}/interfaces/{interfaceName}", auth.PermissionOperationQuery, h.get)
	register("POST /api/v1/switches/{switchID}/interfaces/{interfaceAction}", auth.PermissionOperationConfig, h.adminAction)
	register("PUT /api/v1/switches/{switchID}/interfaces/{interfaceName}/access", auth.PermissionOperationConfig, h.access)
	register("PUT /api/v1/switches/{switchID}/interfaces/{interfaceName}/trunk", auth.PermissionOperationConfig, h.trunk)
	register("POST /api/v1/switches/{switchID}/interfaces/{interfaceName}/vlans", auth.PermissionOperationConfig, h.addVLAN)
	register("DELETE /api/v1/switches/{switchID}/interfaces/{interfaceName}/vlans/{vlanID}", auth.PermissionOperationConfig, h.removeVLAN)
}

func (h *InterfaceHandlers) list(w http.ResponseWriter, r *http.Request) error {
	return h.submitAndWrite(w, r, operation.Request{Name: pluginOperation(pluginapi.OperationInterfaceList), Class: operation.ClassQuery, DeviceID: r.PathValue("switchID"), ExecutionMode: operation.ExecutionModeSync}, operation.ExecutionModeSync)
}

func (h *InterfaceHandlers) get(w http.ResponseWriter, r *http.Request) error {
	name, err := interfaceName(r)
	if err != nil {
		return err
	}
	return h.submitAndWrite(w, r, operation.Request{Name: pluginOperation(pluginapi.OperationInterfaceGet), Class: operation.ClassQuery, DeviceID: r.PathValue("switchID"), Parameters: map[string]any{"interface_name": name}, ExecutionMode: operation.ExecutionModeSync}, operation.ExecutionModeSync)
}

func (h *InterfaceHandlers) adminAction(w http.ResponseWriter, r *http.Request) error {
	action := r.PathValue("interfaceAction")
	var operationName pluginapi.OperationName
	var name string
	switch {
	case strings.HasSuffix(action, ":enable"):
		name = strings.TrimSuffix(action, ":enable")
		operationName = pluginapi.OperationInterfaceEnable
	case strings.HasSuffix(action, ":disable"):
		name = strings.TrimSuffix(action, ":disable")
		operationName = pluginapi.OperationInterfaceDisable
	default:
		return apperror.New(apperror.CodeResourceNotFound, "")
	}
	r.SetPathValue("interfaceName", name)
	return h.adminState(w, r, operationName)
}

func (h *InterfaceHandlers) adminState(w http.ResponseWriter, r *http.Request, name pluginapi.OperationName) error {
	interfaceName, err := interfaceName(r)
	if err != nil {
		return err
	}
	var options vlanOperationOptions
	if err := decodeOptionalStrictJSON(r, &options); err != nil {
		return err
	}
	options.defaults()
	idempotency, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	return h.submitAndWrite(w, r, operation.Request{Name: pluginOperation(name), Class: operation.ClassConfig, DeviceID: r.PathValue("switchID"), Parameters: map[string]any{"interface_name": interfaceName}, ExecutionMode: options.ExecutionMode, DryRun: options.DryRun, SaveConfig: options.SaveConfig, ConfirmRisk: options.ConfirmRisk, IdempotencyKey: idempotency}, options.ExecutionMode)
}

func (h *InterfaceHandlers) access(w http.ResponseWriter, r *http.Request) error {
	name, err := interfaceName(r)
	if err != nil {
		return err
	}
	var body struct {
		VLANID int `json:"vlan_id"`
		vlanOperationOptions
	}
	if err := decodeStrictJSON(r, &body); err != nil {
		return err
	}
	if err := vlan.ValidateID(body.VLANID); err != nil {
		return validationError(err)
	}
	body.defaults()
	return h.submitConfig(w, r, pluginapi.OperationInterfaceAccess, map[string]any{"interface_name": name, "vlan_id": body.VLANID}, body.vlanOperationOptions)
}

func (h *InterfaceHandlers) trunk(w http.ResponseWriter, r *http.Request) error {
	name, err := interfaceName(r)
	if err != nil {
		return err
	}
	var body struct {
		AllowedVLANs []int `json:"allowed_vlans"`
		NativeVLAN   *int  `json:"native_vlan"`
		vlanOperationOptions
	}
	if err := decodeStrictJSON(r, &body); err != nil {
		return err
	}
	allowed, err := switchinterface.NormalizeVLANs(body.AllowedVLANs, true)
	if err != nil {
		return validationError(err)
	}
	parameters := map[string]any{"interface_name": name, "allowed_vlans": allowed}
	if body.NativeVLAN != nil {
		if err := vlan.ValidateID(*body.NativeVLAN); err != nil {
			return validationError(err)
		}
		found := false
		for _, id := range allowed {
			if id == *body.NativeVLAN {
				found = true
				break
			}
		}
		if !found {
			return validationError(errors.New("native_vlan must be included in allowed_vlans"))
		}
		parameters["native_vlan"] = *body.NativeVLAN
	}
	body.defaults()
	return h.submitConfig(w, r, pluginapi.OperationInterfaceTrunk, parameters, body.vlanOperationOptions)
}

func (h *InterfaceHandlers) addVLAN(w http.ResponseWriter, r *http.Request) error {
	name, err := interfaceName(r)
	if err != nil {
		return err
	}
	var body struct {
		VLANID int `json:"vlan_id"`
		vlanOperationOptions
	}
	if err := decodeStrictJSON(r, &body); err != nil {
		return err
	}
	if err := vlan.ValidateID(body.VLANID); err != nil {
		return validationError(err)
	}
	body.defaults()
	return h.submitConfig(w, r, pluginapi.OperationInterfaceVLANAdd, map[string]any{"interface_name": name, "vlan_id": body.VLANID}, body.vlanOperationOptions)
}

func (h *InterfaceHandlers) removeVLAN(w http.ResponseWriter, r *http.Request) error {
	name, err := interfaceName(r)
	if err != nil {
		return err
	}
	id, err := pathVLANID(r)
	if err != nil {
		return err
	}
	var options vlanOperationOptions
	if err := decodeOptionalStrictJSON(r, &options); err != nil {
		return err
	}
	options.defaults()
	return h.submitConfig(w, r, pluginapi.OperationInterfaceVLANRemove, map[string]any{"interface_name": name, "vlan_id": id}, options)
}

func (h *InterfaceHandlers) submitConfig(w http.ResponseWriter, r *http.Request, name pluginapi.OperationName, parameters map[string]any, options vlanOperationOptions) error {
	idempotency, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	return h.submitAndWrite(w, r, operation.Request{Name: pluginOperation(name), Class: operation.ClassConfig, DeviceID: r.PathValue("switchID"), Parameters: parameters, ExecutionMode: options.ExecutionMode, DryRun: options.DryRun, SaveConfig: options.SaveConfig, ConfirmRisk: options.ConfirmRisk, IdempotencyKey: idempotency}, options.ExecutionMode)
}

func (h *InterfaceHandlers) submitAndWrite(w http.ResponseWriter, r *http.Request, request operation.Request, requestedMode operation.ExecutionMode) error {
	actor, err := ActorFromRequest(r)
	if err != nil {
		return apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	request.Actor = actor
	submission, err := h.operations.Submit(r.Context(), operationsvc.SubmitRequest{RequestID: RequestIDFromContext(r.Context()), Operation: request})
	if err != nil {
		return err
	}
	return writeVLANSubmission(w, r, submission, requestedMode)
}

func interfaceName(r *http.Request) (string, error) {
	name := r.PathValue("interfaceName")
	if strings.Contains(name, "%") {
		return "", validationError(errors.New("interface name contains an invalid escape"))
	}
	if err := switchinterface.ValidateNameSafety(name); err != nil {
		return "", validationError(err)
	}
	return name, nil
}

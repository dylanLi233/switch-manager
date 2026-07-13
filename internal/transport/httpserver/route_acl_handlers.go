package httpserver

import (
	"errors"
	"net/http"
	"strings"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/acl"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/route"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

const ExperimentalACLHeader = "X-Experimental-API"

type RouteACLHandlers struct{ operations OperationSubmitter }

func NewRouteACLHandlers(operations OperationSubmitter) (*RouteACLHandlers, error) {
	if operations == nil {
		return nil, errors.New("operation service is required")
	}
	return &RouteACLHandlers{operations: operations}, nil
}

func (h *RouteACLHandlers) Register(mux *http.ServeMux, authenticator Authenticator) {
	if h == nil || h.operations == nil || mux == nil || authenticator == nil {
		return
	}
	register := func(pattern string, permission auth.Permission, experimental bool, handler ErrorHandlerFunc) {
		adapted := AdaptErrorHandler(handler)
		if experimental {
			adapted = experimentalACL(adapted)
		}
		mux.Handle(pattern, AuthenticationMiddleware(authenticator)(RequirePermission(permission, switchScope)(adapted)))
	}
	register("GET /api/v1/switches/{switchID}/routes", auth.PermissionOperationQuery, false, h.listRoutes)
	register("GET /api/v1/switches/{switchID}/routes/{routeID}", auth.PermissionOperationQuery, false, h.getRoute)
	register("POST /api/v1/switches/{switchID}/routes", auth.PermissionOperationConfig, false, h.createRoute)
	register("PATCH /api/v1/switches/{switchID}/routes/{routeID}", auth.PermissionOperationConfig, false, h.updateRoute)
	register("DELETE /api/v1/switches/{switchID}/routes/{routeID}", auth.PermissionOperationConfig, false, h.deleteRoute)
	register("GET /api/v1/switches/{switchID}/acls", auth.PermissionOperationQuery, true, h.listACLs)
	register("GET /api/v1/switches/{switchID}/acls/{aclID}", auth.PermissionOperationQuery, true, h.getACL)
	register("POST /api/v1/switches/{switchID}/acls", auth.PermissionOperationConfig, true, h.createACL)
	register("PATCH /api/v1/switches/{switchID}/acls/{aclID}", auth.PermissionOperationConfig, true, h.updateACL)
	register("DELETE /api/v1/switches/{switchID}/acls/{aclID}", auth.PermissionOperationConfig, true, h.deleteACL)
}

func experimentalACL(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(ExperimentalACLHeader, acl.ExperimentalSchemaVersion)
		w.Header().Set("Warning", `299 - "ACL API schema is experimental and may change"`)
		next.ServeHTTP(w, r)
	})
}

func (h *RouteACLHandlers) listRoutes(w http.ResponseWriter, r *http.Request) error {
	return h.submitAndWrite(w, r, operation.Request{Name: operation.Name(pluginapi.OperationRouteList), Class: operation.ClassQuery, DeviceID: r.PathValue("switchID"), ExecutionMode: operation.ExecutionModeSync}, operation.ExecutionModeSync)
}

func (h *RouteACLHandlers) getRoute(w http.ResponseWriter, r *http.Request) error {
	id, err := routeID(r)
	if err != nil {
		return err
	}
	return h.submitAndWrite(w, r, operation.Request{Name: operation.Name(pluginapi.OperationRouteGet), Class: operation.ClassQuery, DeviceID: r.PathValue("switchID"), Parameters: map[string]any{"route_id": id}, ExecutionMode: operation.ExecutionModeSync}, operation.ExecutionModeSync)
}

type routeBody struct {
	AddressFamily     route.AddressFamily `json:"address_family"`
	Destination       string              `json:"destination"`
	NextHop           string              `json:"next_hop"`
	OutgoingInterface string              `json:"outgoing_interface"`
	Description       string              `json:"description"`
	vlanOperationOptions
}

func (b routeBody) spec() (route.Spec, error) {
	return route.NormalizeSpec(route.Spec{AddressFamily: b.AddressFamily, Destination: b.Destination, NextHop: b.NextHop, OutgoingInterface: b.OutgoingInterface, Description: b.Description})
}

func (h *RouteACLHandlers) createRoute(w http.ResponseWriter, r *http.Request) error {
	var body routeBody
	if err := decodeStrictJSON(r, &body); err != nil {
		return err
	}
	spec, err := body.spec()
	if err != nil {
		return validationError(err)
	}
	body.defaults()
	return h.submitConfig(w, r, pluginapi.OperationRouteCreate, map[string]any{"route": spec}, body.vlanOperationOptions)
}

func (h *RouteACLHandlers) updateRoute(w http.ResponseWriter, r *http.Request) error {
	id, err := routeID(r)
	if err != nil {
		return err
	}
	var body routeBody
	if err := decodeStrictJSON(r, &body); err != nil {
		return err
	}
	spec, err := body.spec()
	if err != nil {
		return validationError(err)
	}
	body.defaults()
	return h.submitConfig(w, r, pluginapi.OperationRouteUpdate, map[string]any{"route_id": id, "route": spec}, body.vlanOperationOptions)
}

func (h *RouteACLHandlers) deleteRoute(w http.ResponseWriter, r *http.Request) error {
	id, err := routeID(r)
	if err != nil {
		return err
	}
	var options vlanOperationOptions
	if err := decodeOptionalStrictJSON(r, &options); err != nil {
		return err
	}
	options.defaults()
	return h.submitConfig(w, r, pluginapi.OperationRouteDelete, map[string]any{"route_id": id}, options)
}

func (h *RouteACLHandlers) listACLs(w http.ResponseWriter, r *http.Request) error {
	return h.submitAndWrite(w, r, operation.Request{Name: operation.Name(pluginapi.OperationACLList), Class: operation.ClassQuery, DeviceID: r.PathValue("switchID"), ExecutionMode: operation.ExecutionModeSync}, operation.ExecutionModeSync)
}

func (h *RouteACLHandlers) getACL(w http.ResponseWriter, r *http.Request) error {
	id, err := aclID(r)
	if err != nil {
		return err
	}
	return h.submitAndWrite(w, r, operation.Request{Name: operation.Name(pluginapi.OperationACLGet), Class: operation.ClassQuery, DeviceID: r.PathValue("switchID"), Parameters: map[string]any{"acl_id": id}, ExecutionMode: operation.ExecutionModeSync}, operation.ExecutionModeSync)
}

type aclBody struct {
	SchemaVersion string            `json:"schema_version"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	AddressFamily acl.AddressFamily `json:"address_family"`
	Rules         []acl.Rule        `json:"rules"`
	vlanOperationOptions
}

func (b aclBody) spec() (acl.Spec, error) {
	return acl.NormalizeSpec(acl.Spec{SchemaVersion: b.SchemaVersion, Name: b.Name, Description: b.Description, AddressFamily: b.AddressFamily, Rules: b.Rules})
}

func (h *RouteACLHandlers) createACL(w http.ResponseWriter, r *http.Request) error {
	var body aclBody
	if err := decodeStrictJSON(r, &body); err != nil {
		return err
	}
	spec, err := body.spec()
	if err != nil {
		return validationError(err)
	}
	body.defaults()
	return h.submitConfig(w, r, pluginapi.OperationACLCreate, map[string]any{"acl": spec}, body.vlanOperationOptions)
}

func (h *RouteACLHandlers) updateACL(w http.ResponseWriter, r *http.Request) error {
	id, err := aclID(r)
	if err != nil {
		return err
	}
	var body aclBody
	if err := decodeStrictJSON(r, &body); err != nil {
		return err
	}
	spec, err := body.spec()
	if err != nil {
		return validationError(err)
	}
	body.defaults()
	return h.submitConfig(w, r, pluginapi.OperationACLUpdate, map[string]any{"acl_id": id, "acl": spec}, body.vlanOperationOptions)
}

func (h *RouteACLHandlers) deleteACL(w http.ResponseWriter, r *http.Request) error {
	id, err := aclID(r)
	if err != nil {
		return err
	}
	var options vlanOperationOptions
	if err := decodeOptionalStrictJSON(r, &options); err != nil {
		return err
	}
	options.defaults()
	return h.submitConfig(w, r, pluginapi.OperationACLDelete, map[string]any{"acl_id": id}, options)
}

func (h *RouteACLHandlers) submitConfig(w http.ResponseWriter, r *http.Request, name pluginapi.OperationName, parameters map[string]any, options vlanOperationOptions) error {
	idempotency, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	return h.submitAndWrite(w, r, operation.Request{Name: operation.Name(name), Class: operation.ClassConfig, DeviceID: r.PathValue("switchID"), Parameters: parameters, ExecutionMode: options.ExecutionMode, DryRun: options.DryRun, SaveConfig: options.SaveConfig, ConfirmRisk: options.ConfirmRisk, IdempotencyKey: idempotency}, options.ExecutionMode)
}

func (h *RouteACLHandlers) submitAndWrite(w http.ResponseWriter, r *http.Request, request operation.Request, requestedMode operation.ExecutionMode) error {
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

func routeID(r *http.Request) (string, error) {
	id := strings.TrimSpace(r.PathValue("routeID"))
	if err := route.ValidateID(id); err != nil {
		return "", validationError(err)
	}
	return id, nil
}

func aclID(r *http.Request) (string, error) {
	id := strings.TrimSpace(r.PathValue("aclID"))
	if err := acl.ValidateID(id); err != nil {
		return "", validationError(err)
	}
	return id, nil
}

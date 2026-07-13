package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/internal/domain/vlan"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

type OperationSubmitter interface {
	Submit(context.Context, operationsvc.SubmitRequest) (operationsvc.Submission, error)
}

type VLANHandlers struct{ operations OperationSubmitter }

func NewVLANHandlers(operations OperationSubmitter) (*VLANHandlers, error) {
	if operations == nil {
		return nil, errors.New("operation service is required")
	}
	return &VLANHandlers{operations: operations}, nil
}

func (h *VLANHandlers) Register(mux *http.ServeMux, authenticator Authenticator) {
	if h == nil || h.operations == nil || mux == nil || authenticator == nil {
		return
	}
	register := func(pattern string, permission auth.Permission, handler ErrorHandlerFunc) {
		wrapped := AuthenticationMiddleware(authenticator)(RequirePermission(permission, switchScope)(AdaptErrorHandler(handler)))
		mux.Handle(pattern, wrapped)
	}
	register("GET /api/v1/switches/{switchID}/vlans", auth.PermissionOperationQuery, h.list)
	register("GET /api/v1/switches/{switchID}/vlans/{vlanID}", auth.PermissionOperationQuery, h.get)
	register("POST /api/v1/switches/{switchID}/vlans", auth.PermissionOperationConfig, h.create)
	register("PATCH /api/v1/switches/{switchID}/vlans/{vlanID}", auth.PermissionOperationConfig, h.update)
	register("DELETE /api/v1/switches/{switchID}/vlans/{vlanID}", auth.PermissionOperationConfig, h.delete)
}

type vlanOperationOptions struct {
	ExecutionMode operation.ExecutionMode `json:"execution_mode"`
	DryRun       bool                    `json:"dry_run"`
	SaveConfig   bool                    `json:"save_config"`
	ConfirmRisk  bool                    `json:"confirm_risk"`
}

func (o *vlanOperationOptions) defaults() {
	if o.ExecutionMode == "" {
		o.ExecutionMode = operation.ExecutionModeSync
	}
}

func (h *VLANHandlers) list(w http.ResponseWriter, r *http.Request) error {
	return h.submitAndWrite(w, r, operation.Request{
		Name: pluginOperation(pluginapi.OperationVLANList), Class: operation.ClassQuery,
		DeviceID: r.PathValue("switchID"), ExecutionMode: operation.ExecutionModeSync,
	}, operation.ExecutionModeSync)
}

func (h *VLANHandlers) get(w http.ResponseWriter, r *http.Request) error {
	id, err := pathVLANID(r)
	if err != nil {
		return err
	}
	return h.submitAndWrite(w, r, operation.Request{
		Name: pluginOperation(pluginapi.OperationVLANGet), Class: operation.ClassQuery,
		DeviceID: r.PathValue("switchID"), Parameters: map[string]any{"vlan_id": id},
		ExecutionMode: operation.ExecutionModeSync,
	}, operation.ExecutionModeSync)
}

func (h *VLANHandlers) create(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		VLANID int `json:"vlan_id"`
		Name string `json:"name"`
		vlanOperationOptions
	}
	if err := decodeStrictJSON(r, &body); err != nil {
		return err
	}
	if err := vlan.ValidateID(body.VLANID); err != nil {
		return validationError(err)
	}
	name, err := vlan.NormalizeName(body.Name, false)
	if err != nil {
		return validationError(err)
	}
	body.defaults()
	idempotency, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	return h.submitAndWrite(w, r, operation.Request{
		Name: pluginOperation(pluginapi.OperationVLANCreate), Class: operation.ClassConfig,
		DeviceID: r.PathValue("switchID"), Parameters: map[string]any{"vlan_id": body.VLANID, "name": name},
		ExecutionMode: body.ExecutionMode, DryRun: body.DryRun, SaveConfig: body.SaveConfig,
		ConfirmRisk: body.ConfirmRisk, IdempotencyKey: idempotency,
	}, body.ExecutionMode)
}

func (h *VLANHandlers) update(w http.ResponseWriter, r *http.Request) error {
	id, err := pathVLANID(r)
	if err != nil {
		return err
	}
	var body struct {
		Name *string `json:"name"`
		vlanOperationOptions
	}
	if err := decodeStrictJSON(r, &body); err != nil {
		return err
	}
	if body.Name == nil {
		return validationError(errors.New("VLAN name is required"))
	}
	name, err := vlan.NormalizeName(*body.Name, true)
	if err != nil {
		return validationError(err)
	}
	body.defaults()
	idempotency, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	return h.submitAndWrite(w, r, operation.Request{
		Name: pluginOperation(pluginapi.OperationVLANUpdate), Class: operation.ClassConfig,
		DeviceID: r.PathValue("switchID"), Parameters: map[string]any{"vlan_id": id, "name": name},
		ExecutionMode: body.ExecutionMode, DryRun: body.DryRun, SaveConfig: body.SaveConfig,
		ConfirmRisk: body.ConfirmRisk, IdempotencyKey: idempotency,
	}, body.ExecutionMode)
}

func (h *VLANHandlers) delete(w http.ResponseWriter, r *http.Request) error {
	id, err := pathVLANID(r)
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
	return h.submitAndWrite(w, r, operation.Request{
		Name: pluginOperation(pluginapi.OperationVLANDelete), Class: operation.ClassConfig,
		DeviceID: r.PathValue("switchID"), Parameters: map[string]any{"vlan_id": id},
		ExecutionMode: options.ExecutionMode, DryRun: options.DryRun, SaveConfig: options.SaveConfig,
		ConfirmRisk: options.ConfirmRisk, IdempotencyKey: idempotency,
	}, options.ExecutionMode)
}

func (h *VLANHandlers) submitAndWrite(w http.ResponseWriter, r *http.Request, request operation.Request, requestedMode operation.ExecutionMode) error {
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

func writeVLANSubmission(w http.ResponseWriter, r *http.Request, submission operationsvc.Submission, requestedMode operation.ExecutionMode) error {
	if requestedMode == operation.ExecutionModeAsync || submission.Deferred || !submission.Completed {
		WriteSuccess(w, r, http.StatusAccepted, map[string]any{
			"task_id": submission.Task.ID, "status": submission.Task.Status,
			"idempotent_replay": submission.IdempotentReplay, "deferred": submission.Deferred,
		})
		return nil
	}
	if submission.Task.Status == task.StatusFailed {
		code := apperror.Code(submission.Task.ErrorCode)
		if _, ok := apperror.Lookup(code); !ok {
			code = apperror.CodeInternalError
		}
		return apperror.New(code, "")
	}
	if submission.Task.Status == task.StatusCancelled || submission.Task.Status == task.StatusInterrupted {
		return apperror.New(apperror.CodeStateConflict, "")
	}
	var stored struct {
		DryRun bool `json:"dry_run"`
		Plan any `json:"plan"`
		Operation *struct {
			Data any `json:"data"`
		} `json:"operation"`
		SaveConfig any `json:"save_config"`
		AuditCompleted bool `json:"audit_completed"`
	}
	if len(submission.Task.Result) > 0 {
		if err := json.Unmarshal(submission.Task.Result, &stored); err != nil {
			return apperror.Wrap(apperror.CodeInternalError, "", err)
		}
	}
	response := map[string]any{
		"task_id": submission.Task.ID, "status": submission.Task.Status,
		"error_code": submission.Task.ErrorCode, "plugin_name": submission.Task.PluginName,
		"plugin_version": submission.Task.PluginVersion, "dry_run": stored.DryRun,
		"audit_completed": stored.AuditCompleted, "idempotent_replay": submission.IdempotentReplay,
	}
	if stored.DryRun {
		response["plan"] = stored.Plan
	} else if stored.Operation != nil {
		response["result"] = stored.Operation.Data
	}
	if stored.SaveConfig != nil {
		response["save_config"] = stored.SaveConfig
	}
	WriteSuccess(w, r, http.StatusOK, response)
	return nil
}

func pluginOperation(name pluginapi.OperationName) operation.Name { return operation.Name(name) }

func pathVLANID(r *http.Request) (int, error) {
	id, err := strconv.Atoi(strings.TrimSpace(r.PathValue("vlanID")))
	if err != nil {
		return 0, validationError(errors.New("vlan_id must be an integer"))
	}
	if err := vlan.ValidateID(id); err != nil {
		return 0, validationError(err)
	}
	return id, nil
}

func idempotencyKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) == 0 {
		return "", nil
	}
	if len(values) != 1 {
		return "", validationError(errors.New("exactly one Idempotency-Key header is allowed"))
	}
	value := values[0]
	if value == "" || value != strings.TrimSpace(value) || len(value) > 128 {
		return "", validationError(errors.New("Idempotency-Key must contain 1-128 non-whitespace characters"))
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", validationError(errors.New("Idempotency-Key contains a control character"))
		}
	}
	return value, nil
}

func decodeOptionalStrictJSON(r *http.Request, destination any) error {
	if r == nil || r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return nil
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return validationError(err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return validationError(errors.New("request body must contain one JSON object"))
	}
	return nil
}

func validationError(err error) error { return apperror.Wrap(apperror.CodeValidationError, "", err) }

package httpserver

import (
	"errors"
	"net/http"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

type CustomCommandHandlers struct{ operations OperationSubmitter }

func NewCustomCommandHandlers(operations OperationSubmitter) (*CustomCommandHandlers, error) {
	if operations == nil {
		return nil, errors.New("operation service is required")
	}
	return &CustomCommandHandlers{operations: operations}, nil
}

func (h *CustomCommandHandlers) Register(mux *http.ServeMux, authenticator Authenticator) {
	if h == nil || h.operations == nil || mux == nil || authenticator == nil {
		return
	}
	register := func(pattern string, permission auth.Permission, handler ErrorHandlerFunc) {
		wrapped := AuthenticationMiddleware(authenticator)(RequirePermission(permission, switchScope)(AdaptErrorHandler(handler)))
		mux.Handle(pattern, wrapped)
	}
	register("POST /api/v1/switches/{switchID}/commands:readonly", auth.PermissionOperationCustomRead, h.readonly)
	register("POST /api/v1/switches/{switchID}/commands:config", auth.PermissionOperationCustomConfig, h.config)
}

type customCommandRequest struct {
	Commands      []string                `json:"commands"`
	ExecutionMode operation.ExecutionMode `json:"execution_mode"`
	DryRun       bool                    `json:"dry_run"`
	SaveConfig   bool                    `json:"save_config"`
	ConfirmRisk  bool                    `json:"confirm_risk"`
}

func (r *customCommandRequest) defaults() {
	if r.ExecutionMode == "" {
		r.ExecutionMode = operation.ExecutionModeSync
	}
}

func (h *CustomCommandHandlers) readonly(w http.ResponseWriter, r *http.Request) error {
	var body customCommandRequest
	if err := decodeStrictJSON(r, &body); err != nil {
		return err
	}
	body.defaults()
	if body.SaveConfig {
		return validationError(errors.New("save_config is not valid for a read-only command"))
	}
	return h.submit(w, r, body, pluginapi.OperationCommandExecuteReadonly, operation.ClassQuery)
}

func (h *CustomCommandHandlers) config(w http.ResponseWriter, r *http.Request) error {
	var body customCommandRequest
	if err := decodeStrictJSON(r, &body); err != nil {
		return err
	}
	body.defaults()
	return h.submit(w, r, body, pluginapi.OperationCommandExecuteConfig, operation.ClassConfig)
}

func (h *CustomCommandHandlers) submit(w http.ResponseWriter, r *http.Request, body customCommandRequest, name pluginapi.OperationName, class operation.Class) error {
	if len(body.Commands) == 0 {
		return validationError(errors.New("at least one command is required"))
	}
	idempotency, err := idempotencyKey(r)
	if err != nil {
		return err
	}
	actor, err := ActorFromRequest(r)
	if err != nil {
		return apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	request := operation.Request{
		Name: operation.Name(name), Class: class, DeviceID: r.PathValue("switchID"),
		Parameters: map[string]any{"commands": append([]string(nil), body.Commands...)},
		ExecutionMode: body.ExecutionMode, DryRun: body.DryRun, SaveConfig: body.SaveConfig,
		ConfirmRisk: body.ConfirmRisk, IdempotencyKey: idempotency, Actor: actor,
	}
	submission, err := h.operations.Submit(r.Context(), operationsvc.SubmitRequest{RequestID: RequestIDFromContext(r.Context()), Operation: request})
	if err != nil {
		return err
	}
	return writeVLANSubmission(w, r, submission, body.ExecutionMode)
}

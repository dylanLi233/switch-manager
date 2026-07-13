package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/internal/operationsvc"
)

type BatchOperationService interface {
	Submit(context.Context, operationsvc.BatchSubmitRequest) (task.BatchSnapshot, error)
	Get(context.Context, string) (task.BatchSnapshot, error)
	Cancel(context.Context, string) (task.BatchSnapshot, error)
	RetryFailed(context.Context, string, string, auth.Actor) (task.BatchSnapshot, error)
}

type BatchHandlers struct{ service BatchOperationService }

func NewBatchHandlers(service BatchOperationService) (*BatchHandlers, error) {
	if service == nil {
		return nil, errors.New("batch operation service is required")
	}
	return &BatchHandlers{service: service}, nil
}

func (h *BatchHandlers) Register(mux *http.ServeMux, authenticator Authenticator) {
	if h == nil || h.service == nil || mux == nil || authenticator == nil {
		return
	}
	mux.Handle("POST /api/v1/batch-operations", AuthenticationMiddleware(authenticator)(AdaptErrorHandler(h.create)))
	mux.Handle("GET /api/v1/batch-operations/{batchID}", AuthenticationMiddleware(authenticator)(RequirePermission(auth.PermissionTaskRead, globalScope)(AdaptErrorHandler(h.get))))
	mux.Handle("POST /api/v1/batch-operations/{batchAction}", AuthenticationMiddleware(authenticator)(RequirePermission(auth.PermissionTaskCancel, globalScope)(AdaptErrorHandler(h.action))))
}

type createBatchBody struct {
	TargetSwitchIDs   []string       `json:"target_switch_ids"`
	Operation         operation.Name `json:"operation"`
	Parameters        map[string]any `json:"parameters"`
	ContinueOnFailure *bool          `json:"continue_on_failure"`
	DryRun            bool           `json:"dry_run"`
	SaveConfig        bool           `json:"save_config"`
	ConfirmRisk       bool           `json:"confirm_risk"`
}

func (h *BatchHandlers) create(w http.ResponseWriter, r *http.Request) error {
	var body createBatchBody
	if err := decodeStrictJSON(r, &body); err != nil {
		return err
	}
	continueOnFailure := true
	if body.ContinueOnFailure != nil {
		continueOnFailure = *body.ContinueOnFailure
	}
	class, err := operationsvc.BatchOperationClass(body.Operation)
	if err != nil {
		return validationError(err)
	}
	permission := auth.PermissionOperationQuery
	if class == operation.ClassConfig {
		permission = auth.PermissionOperationConfig
	}
	request, actor, err := authorizeBatchTargets(r, permission, body.TargetSwitchIDs)
	if err != nil {
		return err
	}
	snapshot, err := h.service.Submit(request.Context(), operationsvc.BatchSubmitRequest{
		RequestID: RequestIDFromContext(request.Context()), TargetSwitchIDs: body.TargetSwitchIDs,
		Operation: body.Operation, Parameters: body.Parameters, ContinueOnFailure: continueOnFailure,
		DryRun: body.DryRun, SaveConfig: body.SaveConfig, ConfirmRisk: body.ConfirmRisk, Actor: actor,
	})
	if err != nil {
		return err
	}
	WriteSuccess(w, request, http.StatusAccepted, batchResponse(snapshot))
	return nil
}

func authorizeBatchTargets(r *http.Request, permission auth.Permission, deviceIDs []string) (*http.Request, auth.Actor, error) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		return r, auth.Actor{}, apperror.New(apperror.CodeUnauthorized, "")
	}
	if len(deviceIDs) == 0 {
		return r, auth.Actor{}, apperror.New(apperror.CodeValidationError, "")
	}
	selected := auth.Role("")
	for _, id := range deviceIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return r, auth.Actor{}, apperror.New(apperror.CodeValidationError, "")
		}
		role, allowed := principal.Authorize(permission, auth.Scope{Type: auth.ScopeSpecificResource, ID: id})
		if !allowed {
			return r, auth.Actor{}, apperror.New(apperror.CodeForbidden, "")
		}
		if batchRoleRank(role) > batchRoleRank(selected) {
			selected = role
		}
	}
	ctx := context.WithValue(r.Context(), authorizedRoleContextKey{}, selected)
	request := r.WithContext(ctx)
	actor, err := ActorFromRequest(request)
	if err != nil {
		return request, auth.Actor{}, apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	return request, actor, nil
}

func batchRoleRank(role auth.Role) int {
	switch role {
	case auth.RoleAdmin:
		return 3
	case auth.RoleAuditor:
		return 2
	case auth.RoleViewer:
		return 1
	default:
		return 0
	}
}

func (h *BatchHandlers) get(w http.ResponseWriter, r *http.Request) error {
	snapshot, err := h.service.Get(r.Context(), strings.TrimSpace(r.PathValue("batchID")))
	if err != nil {
		return err
	}
	WriteSuccess(w, r, http.StatusOK, batchResponse(snapshot))
	return nil
}

func (h *BatchHandlers) action(w http.ResponseWriter, r *http.Request) error {
	value := strings.TrimSpace(r.PathValue("batchAction"))
	batchID, action, ok := strings.Cut(value, ":")
	if !ok || strings.TrimSpace(batchID) == "" {
		return apperror.New(apperror.CodeValidationError, "")
	}
	var snapshot task.BatchSnapshot
	var err error
	switch action {
	case "cancel":
		snapshot, err = h.service.Cancel(r.Context(), batchID)
	case "retry-failed":
		actor, actorErr := ActorFromRequest(r)
		if actorErr != nil {
			return apperror.Wrap(apperror.CodeInternalError, "", actorErr)
		}
		snapshot, err = h.service.RetryFailed(r.Context(), batchID, RequestIDFromContext(r.Context()), actor)
	default:
		return apperror.New(apperror.CodeValidationError, "")
	}
	if err != nil {
		return err
	}
	WriteSuccess(w, r, http.StatusAccepted, batchResponse(snapshot))
	return nil
}

type batchItemResponse struct {
	SequenceNo int             `json:"sequence_no"`
	DeviceID   string          `json:"device_id"`
	ChildTaskID string         `json:"child_task_id"`
	Status     task.Status     `json:"status"`
	ErrorCode  string          `json:"error_code,omitempty"`
	RetryOf    string          `json:"retry_of,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
}

type batchViewResponse struct {
	BatchID            string              `json:"batch_id"`
	ParentTaskID       string              `json:"parent_task_id"`
	RetryOfBatchID     string              `json:"retry_of_batch_id,omitempty"`
	Operation          operation.Name      `json:"operation"`
	Status             task.Status         `json:"status"`
	ContinueOnFailure  bool                `json:"continue_on_failure"`
	TotalCount         int                 `json:"total_count"`
	SuccessCount       int                 `json:"success_count"`
	FailedCount        int                 `json:"failed_count"`
	CancelledCount     int                 `json:"cancelled_count"`
	Items              []batchItemResponse `json:"items"`
}

func batchResponse(snapshot task.BatchSnapshot) batchViewResponse {
	items := make([]batchItemResponse, len(snapshot.Items))
	for index, item := range snapshot.Items {
		items[index] = batchItemResponse{SequenceNo: item.Item.SequenceNo, DeviceID: item.Item.DeviceID, ChildTaskID: item.Item.ChildTaskID, Status: item.Child.Status, ErrorCode: item.Child.ErrorCode, RetryOf: item.Child.RetryOf, Result: append(json.RawMessage(nil), item.Child.Result...)}
	}
	return batchViewResponse{BatchID: snapshot.Batch.ID, ParentTaskID: snapshot.Batch.ParentTaskID, RetryOfBatchID: snapshot.Parent.RetryOf, Operation: snapshot.Batch.Operation, Status: snapshot.Batch.Status, ContinueOnFailure: snapshot.Batch.ContinueOnFailure, TotalCount: snapshot.Batch.TotalCount, SuccessCount: snapshot.Batch.SuccessCount, FailedCount: snapshot.Batch.FailedCount, CancelledCount: snapshot.Batch.CancelledCount, Items: items}
}

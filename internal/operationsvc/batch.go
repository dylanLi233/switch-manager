package operationsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/audit"
	"github.com/dylanLi233/switch-manager/internal/domain/auth"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

const batchPayloadVersion = 1

type BatchCommit struct {
	Parent      task.Persisted
	ParentAudit audit.Record
	Batch       task.Batch
	Children    []task.Persisted
	Items       []task.BatchItem
	Audits      []audit.Record
}

type BatchPersistence interface {
	CreateBatch(context.Context, BatchCommit) (task.BatchSnapshot, error)
	GetBatch(context.Context, string) (task.BatchSnapshot, error)
	RefreshBatch(context.Context, string, time.Time) (task.BatchSnapshot, error)
	ListOpenBatchIDs(context.Context, int) ([]string, error)
}

type BatchQueue interface {
	Queue(context.Context, string) (task.Persisted, error)
	Cancel(context.Context, string) (task.Persisted, error)
}

type BatchSubmitRequest struct {
	RequestID          string
	TargetSwitchIDs    []string
	Operation          operation.Name
	Parameters         map[string]any
	ContinueOnFailure  bool
	DryRun             bool
	SaveConfig         bool
	ConfirmRisk        bool
	Actor              auth.Actor
}

func (r BatchSubmitRequest) Validate() error {
	if strings.TrimSpace(r.RequestID) == "" {
		return errors.New("request ID is required")
	}
	if err := r.Actor.Validate(); err != nil {
		return err
	}
	if len(r.TargetSwitchIDs) < 1 || len(r.TargetSwitchIDs) > 500 {
		return errors.New("batch must target between 1 and 500 switches")
	}
	seen := make(map[string]struct{}, len(r.TargetSwitchIDs))
	for _, id := range r.TargetSwitchIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return errors.New("target switch ID is required")
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate target switch %q", id)
		}
		seen[id] = struct{}{}
	}
	if _, err := BatchOperationClass(r.Operation); err != nil {
		return err
	}
	if r.Parameters != nil {
		if _, err := json.Marshal(r.Parameters); err != nil {
			return fmt.Errorf("batch parameters must be JSON serializable: %w", err)
		}
	}
	return nil
}

type batchPayload struct {
	Version             int            `json:"version"`
	TargetSwitchIDs     []string       `json:"target_switch_ids"`
	Operation           operation.Name `json:"operation"`
	Parameters          map[string]any `json:"parameters,omitempty"`
	ContinueOnFailure   bool           `json:"continue_on_failure"`
	DryRun              bool           `json:"dry_run"`
	SaveConfig          bool           `json:"save_config"`
	ConfirmRisk         bool           `json:"confirm_risk"`
	RequestID           string         `json:"request_id"`
}

type BatchService struct {
	persistence BatchPersistence
	planner      *Planner
	queue        BatchQueue
	config       Config
	ids          IDGenerator
	now          func() time.Time
}

func NewBatchService(persistence BatchPersistence, planner *Planner, queue BatchQueue, config Config) (*BatchService, error) {
	if persistence == nil {
		return nil, errors.New("batch persistence is required")
	}
	if planner == nil {
		return nil, errors.New("operation planner is required")
	}
	if queue == nil {
		return nil, errors.New("batch task queue is required")
	}
	return &BatchService{persistence: persistence, planner: planner, queue: queue, config: config.withDefaults(), ids: randomUUID, now: time.Now}, nil
}

func BatchOperationClass(name operation.Name) (operation.Class, error) {
	switch pluginapi.OperationName(name) {
	case pluginapi.OperationVLANList, pluginapi.OperationVLANGet,
		pluginapi.OperationInterfaceList, pluginapi.OperationInterfaceGet,
		pluginapi.OperationRouteList, pluginapi.OperationRouteGet,
		pluginapi.OperationACLList, pluginapi.OperationACLGet,
		pluginapi.OperationMACTableList, pluginapi.OperationARPTableList,
		pluginapi.OperationDeviceStatusGet,
		pluginapi.OperationName("diagnostic.echo"):
		return operation.ClassQuery, nil
	case pluginapi.OperationVLANCreate, pluginapi.OperationVLANUpdate, pluginapi.OperationVLANDelete,
		pluginapi.OperationInterfaceEnable, pluginapi.OperationInterfaceDisable,
		pluginapi.OperationInterfaceAccess, pluginapi.OperationInterfaceTrunk,
		pluginapi.OperationInterfaceVLANAdd, pluginapi.OperationInterfaceVLANRemove,
		pluginapi.OperationRouteCreate, pluginapi.OperationRouteUpdate, pluginapi.OperationRouteDelete,
		pluginapi.OperationACLCreate, pluginapi.OperationACLUpdate, pluginapi.OperationACLDelete,
		pluginapi.OperationName("configuration.echo"):
		return operation.ClassConfig, nil
	default:
		return "", fmt.Errorf("operation %q is not supported by batch V1", name)
	}
}

func (s *BatchService) Submit(ctx context.Context, request BatchSubmitRequest) (task.BatchSnapshot, error) {
	return s.submit(ctx, request, "", nil)
}

func (s *BatchService) submit(ctx context.Context, request BatchSubmitRequest, retryOfParent string, retryOfChildren map[string]string) (task.BatchSnapshot, error) {
	if ctx == nil {
		return task.BatchSnapshot{}, errors.New("context is required")
	}
	if err := request.Validate(); err != nil {
		return task.BatchSnapshot{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	class, _ := BatchOperationClass(request.Operation)
	targets := append([]string(nil), request.TargetSwitchIDs...)
	for i := range targets {
		targets[i] = strings.TrimSpace(targets[i])
	}
	sort.Strings(targets)
	now := s.now().UTC()
	batchID, err := s.newID()
	if err != nil {
		return task.BatchSnapshot{}, err
	}
	startedAt := now
	parentPayload, err := json.Marshal(batchPayload{Version: batchPayloadVersion, TargetSwitchIDs: targets, Operation: request.Operation, Parameters: cloneMap(request.Parameters), ContinueOnFailure: request.ContinueOnFailure, DryRun: request.DryRun, SaveConfig: request.SaveConfig, ConfirmRisk: request.ConfirmRisk, RequestID: request.RequestID})
	if err != nil {
		return task.BatchSnapshot{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	parent := task.Persisted{Task: task.Task{ID: batchID, Type: task.TypeBatchParent, Operation: request.Operation, TargetType: "batch", TargetID: batchID, Status: task.StatusRunning, ExecutionMode: operation.ExecutionModeAsync, Payload: parentPayload, CreatedBy: request.Actor.UserID, RetryOf: retryOfParent, CreatedAt: now, StartedAt: &startedAt, Version: 1}}
	batch := task.Batch{ID: batchID, ParentTaskID: batchID, Operation: request.Operation, TotalCount: len(targets), Status: task.StatusPending, ContinueOnFailure: request.ContinueOnFailure, CreatedBy: request.Actor.UserID, CreatedAt: now, UpdatedAt: now}
	parentRequest, _ := json.Marshal(map[string]any{"target_switch_ids": targets, "operation": request.Operation, "parameters": request.Parameters, "continue_on_failure": request.ContinueOnFailure, "dry_run": request.DryRun, "save_config": request.SaveConfig, "confirm_risk": request.ConfirmRisk})
	parentAudit := audit.Record{ID: batchID, RequestID: request.RequestID, TaskID: batchID, ActorUserID: request.Actor.UserID, ActorUsername: request.Actor.Username, ActorRole: string(request.Actor.Role), ServiceActorID: request.Actor.ServiceActorID, SourceIP: request.Actor.SourceIP, Action: "batch:" + string(request.Operation), TargetType: "batch", TargetID: batchID, RequestPayloadRedacted: parentRequest, CommandPlanRedacted: json.RawMessage(`{"child_plans":[]}`), Status: string(task.StatusPending), CreatedAt: now}

	children := make([]task.Persisted, 0, len(targets))
	items := make([]task.BatchItem, 0, len(targets))
	audits := make([]audit.Record, 0, len(targets))
	parentPlans := make([]map[string]any, 0, len(targets))
	for index, deviceID := range targets {
		childID, err := s.newID()
		if err != nil {
			return task.BatchSnapshot{}, err
		}
		mainPlanID, err := s.newID()
		if err != nil {
			return task.BatchSnapshot{}, err
		}
		savePlanID := ""
		if request.SaveConfig {
			savePlanID, err = s.newID()
			if err != nil {
				return task.BatchSnapshot{}, err
			}
		}
		opRequest := operation.Request{Name: request.Operation, Class: class, DeviceID: deviceID, Parameters: cloneMap(request.Parameters), ExecutionMode: operation.ExecutionModeAsync, DryRun: request.DryRun, SaveConfig: request.SaveConfig, ConfirmRisk: request.ConfirmRisk, Actor: request.Actor}
		if err := opRequest.Validate(); err != nil {
			return task.BatchSnapshot{}, apperror.Wrap(apperror.CodeValidationError, "", err)
		}
		prepared, err := s.planner.prepare(ctx, planInput{Name: request.Operation, Class: class, DeviceID: deviceID, Parameters: request.Parameters, SaveConfig: request.SaveConfig, ConfirmRisk: request.ConfirmRisk, MainPlanID: mainPlanID, SavePlanID: savePlanID})
		if err != nil {
			return task.BatchSnapshot{}, err
		}
		snapshot := snapshotRequest(opRequest)
		requestFingerprint, err := fingerprint(snapshot)
		if err != nil {
			return task.BatchSnapshot{}, apperror.Wrap(apperror.CodeValidationError, "", err)
		}
		payload, err := encodeTaskPayload(taskPayload{Version: payloadVersion, Request: snapshot, RequestFingerprint: requestFingerprint, RequestID: request.RequestID, MainPlanID: mainPlanID, MainPlanSHA256: prepared.MainHash, SavePlanID: savePlanID, SavePlanSHA256: prepared.SaveHash})
		if err != nil {
			return task.BatchSnapshot{}, apperror.Wrap(apperror.CodeValidationError, "", err)
		}
		auditRequest, err := marshalAuditRequest(opRequest, requestFingerprint)
		if err != nil {
			return task.BatchSnapshot{}, apperror.Wrap(apperror.CodeInternalError, "", err)
		}
		child := task.Persisted{Task: task.Task{ID: childID, ParentTaskID: batchID, Type: task.TypeBatchChild, Operation: request.Operation, TargetType: "switch", TargetID: deviceID, Status: task.StatusPending, ExecutionMode: operation.ExecutionModeAsync, Payload: payload, CreatedBy: request.Actor.UserID, RetryOf: retryOfChildren[deviceID], PluginName: prepared.Metadata.Name, PluginVersion: prepared.Metadata.PluginVersion.String(), CreatedAt: now, Version: 1}, DryRun: request.DryRun, SaveConfig: request.SaveConfig}
		childAudit := audit.Record{ID: childID, RequestID: request.RequestID, TaskID: childID, ActorUserID: request.Actor.UserID, ActorUsername: request.Actor.Username, ActorRole: string(request.Actor.Role), ServiceActorID: request.Actor.ServiceActorID, SourceIP: request.Actor.SourceIP, Action: string(request.Operation), TargetType: "switch", TargetID: deviceID, DeviceVendor: string(prepared.Device.Vendor), DeviceModel: prepared.Device.Model, DeviceOSVersion: prepared.Device.OSVersion, PluginName: prepared.Metadata.Name, PluginVersion: prepared.Metadata.PluginVersion.String(), RequestPayloadRedacted: auditRequest, CommandPlanRedacted: json.RawMessage(prepared.AuditPlan), Status: string(task.StatusPending), CreatedAt: now}
		children = append(children, child)
		items = append(items, task.BatchItem{BatchTaskID: batchID, DeviceID: deviceID, ChildTaskID: childID, SequenceNo: index + 1})
		audits = append(audits, childAudit)
		parentPlans = append(parentPlans, map[string]any{"device_id": deviceID, "plan": json.RawMessage(prepared.AuditPlan)})
	}
	parentPlan, _ := json.Marshal(map[string]any{"child_plans": parentPlans})
	parentAudit.CommandPlanRedacted = parentPlan
	created, err := s.persistence.CreateBatch(ctx, BatchCommit{Parent: parent, ParentAudit: parentAudit, Batch: batch, Children: children, Items: items, Audits: audits})
	if err != nil {
		return task.BatchSnapshot{}, err
	}
	for _, item := range created.Items {
		if _, err := s.queue.Queue(ctx, item.Child.ID); err != nil {
			return created, err
		}
	}
	return s.persistence.RefreshBatch(ctx, batchID, s.now().UTC())
}

func (s *BatchService) Get(ctx context.Context, id string) (task.BatchSnapshot, error) {
	if ctx == nil {
		return task.BatchSnapshot{}, errors.New("context is required")
	}
	return s.persistence.RefreshBatch(ctx, strings.TrimSpace(id), s.now().UTC())
}

func (s *BatchService) Cancel(ctx context.Context, id string) (task.BatchSnapshot, error) {
	snapshot, err := s.Get(ctx, id)
	if err != nil {
		return task.BatchSnapshot{}, err
	}
	if snapshot.Batch.Status.Terminal() {
		return task.BatchSnapshot{}, apperror.New(apperror.CodeTaskNotCancellable, "batch is already terminal")
	}
	for _, item := range snapshot.Items {
		if item.Child.Status.Terminal() {
			continue
		}
		if _, cancelErr := s.queue.Cancel(ctx, item.Child.ID); cancelErr != nil && !apperror.IsCode(cancelErr, apperror.CodeTaskNotCancellable) {
			return task.BatchSnapshot{}, cancelErr
		}
	}
	return s.persistence.RefreshBatch(ctx, snapshot.Batch.ID, s.now().UTC())
}

func (s *BatchService) RetryFailed(ctx context.Context, sourceID, requestID string, actor auth.Actor) (task.BatchSnapshot, error) {
	if err := actor.Validate(); err != nil {
		return task.BatchSnapshot{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	source, err := s.Get(ctx, sourceID)
	if err != nil {
		return task.BatchSnapshot{}, err
	}
	if !source.Batch.Status.Terminal() {
		return task.BatchSnapshot{}, apperror.New(apperror.CodeStateConflict, "batch is not terminal")
	}
	var targets []string
	retryMap := make(map[string]string)
	for _, item := range source.Items {
		if task.RetryableBatchChild(item.Child.Status) {
			targets = append(targets, item.Item.DeviceID)
			retryMap[item.Item.DeviceID] = item.Child.ID
		}
	}
	if len(targets) == 0 {
		return task.BatchSnapshot{}, apperror.New(apperror.CodeStateConflict, "batch has no failed or interrupted children")
	}
	firstPayload, err := decodeTaskPayload(source.Items[0].Child)
	if err != nil {
		return task.BatchSnapshot{}, apperror.Wrap(apperror.CodeStateConflict, "", err)
	}
	request := BatchSubmitRequest{RequestID: requestID, TargetSwitchIDs: targets, Operation: firstPayload.Request.Name, Parameters: cloneMap(firstPayload.Request.Parameters), ContinueOnFailure: source.Batch.ContinueOnFailure, DryRun: firstPayload.Request.DryRun, SaveConfig: firstPayload.Request.SaveConfig, ConfirmRisk: firstPayload.Request.ConfirmRisk, Actor: actor}
	return s.submit(ctx, request, source.Parent.ID, retryMap)
}

func (s *BatchService) Recover(ctx context.Context) error {
	ids, err := s.persistence.ListOpenBatchIDs(ctx, 1000)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := s.persistence.RefreshBatch(ctx, id, s.now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

// AfterTaskFinalized implements scheduler.CompletionObserver.
func (s *BatchService) AfterTaskFinalized(ctx context.Context, value task.Persisted) error {
	if value.Type != task.TypeBatchChild || strings.TrimSpace(value.ParentTaskID) == "" {
		return nil
	}
	_, err := s.persistence.RefreshBatch(ctx, value.ParentTaskID, s.now().UTC())
	return err
}

func (s *BatchService) newID() (string, error) {
	id, err := s.ids()
	if err != nil {
		return "", apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	if strings.TrimSpace(id) == "" {
		return "", apperror.New(apperror.CodeInternalError, "")
	}
	return id, nil
}

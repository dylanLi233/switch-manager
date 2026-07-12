package operationsvc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/audit"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
)

// Service validates and durably submits operations to the scheduler.
type Service struct {
	tasks    task.Repository
	audits   audit.Repository
	planner  *Planner
	queue    Queue
	config   Config
	ids      IDGenerator
	now      func() time.Time
	reporter AuditFailureReporter
}

func NewService(tasks task.Repository, audits audit.Repository, planner *Planner, queue Queue, config Config) (*Service, error) {
	if tasks == nil {
		return nil, errors.New("task repository is required")
	}
	if audits == nil {
		return nil, errors.New("audit repository is required")
	}
	if planner == nil {
		return nil, errors.New("operation planner is required")
	}
	if queue == nil {
		return nil, errors.New("task queue is required")
	}
	return &Service{tasks: tasks, audits: audits, planner: planner, queue: queue, config: config.withDefaults(), ids: randomUUID, now: time.Now}, nil
}

func (s *Service) SetAuditFailureReporter(reporter AuditFailureReporter) {
	if s != nil {
		s.reporter = reporter
	}
}

func (s *Service) Submit(ctx context.Context, request SubmitRequest) (Submission, error) {
	if ctx == nil {
		return Submission{}, errors.New("context is required")
	}
	if err := request.Validate(); err != nil {
		return Submission{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	snapshot := snapshotRequest(request.Operation)
	requestFingerprint, err := fingerprint(snapshot)
	if err != nil {
		return Submission{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}

	if request.Operation.IdempotencyKey != "" {
		existing, findErr := s.tasks.FindByIdempotency(ctx, request.Operation.Actor.UserID, request.Operation.IdempotencyKey)
		switch {
		case findErr == nil:
			return s.replay(ctx, existing, requestFingerprint, request.Operation.ExecutionMode)
		case apperror.IsCode(findErr, apperror.CodeTaskNotFound):
		default:
			return Submission{}, findErr
		}
	}

	taskID, err := s.newID()
	if err != nil {
		return Submission{}, err
	}
	mainPlanID, err := s.newID()
	if err != nil {
		return Submission{}, err
	}
	savePlanID := ""
	if request.Operation.SaveConfig {
		savePlanID, err = s.newID()
		if err != nil {
			return Submission{}, err
		}
	}

	prepared, err := s.planner.prepare(ctx, planInput{Name: request.Operation.Name, Class: request.Operation.Class, DeviceID: request.Operation.DeviceID, Parameters: request.Operation.Parameters, SaveConfig: request.Operation.SaveConfig, ConfirmRisk: request.Operation.ConfirmRisk, MainPlanID: mainPlanID, SavePlanID: savePlanID})
	if err != nil {
		return Submission{}, err
	}

	payload, err := encodeTaskPayload(taskPayload{Version: payloadVersion, Request: snapshot, RequestFingerprint: requestFingerprint, RequestID: request.RequestID, MainPlanID: mainPlanID, MainPlanSHA256: prepared.MainHash, SavePlanID: savePlanID, SavePlanSHA256: prepared.SaveHash})
	if err != nil {
		return Submission{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	auditRequest, err := marshalAuditRequest(request.Operation, requestFingerprint)
	if err != nil {
		return Submission{}, apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	now := s.now().UTC()
	created, err := s.tasks.Create(ctx, task.Persisted{Task: task.Task{ID: taskID, Type: task.TypeOperation, Operation: request.Operation.Name, TargetType: "switch", TargetID: request.Operation.DeviceID, Status: task.StatusPending, ExecutionMode: request.Operation.ExecutionMode, Payload: payload, CreatedBy: request.Operation.Actor.UserID, PluginName: prepared.Metadata.Name, PluginVersion: prepared.Metadata.PluginVersion.String(), CreatedAt: now, Version: 1}, DryRun: request.Operation.DryRun, SaveConfig: request.Operation.SaveConfig, IdempotencyKey: request.Operation.IdempotencyKey})
	if err != nil {
		if request.Operation.IdempotencyKey != "" && apperror.IsCode(err, apperror.CodeIdempotencyConflict) {
			existing, findErr := s.tasks.FindByIdempotency(ctx, request.Operation.Actor.UserID, request.Operation.IdempotencyKey)
			if findErr != nil {
				return Submission{}, err
			}
			return s.replay(ctx, existing, requestFingerprint, request.Operation.ExecutionMode)
		}
		return Submission{}, err
	}

	auditCreated := true
	_, auditErr := s.audits.Create(ctx, audit.Record{ID: created.ID, RequestID: request.RequestID, TaskID: created.ID, ActorUserID: request.Operation.Actor.UserID, ActorUsername: request.Operation.Actor.Username, ActorRole: string(request.Operation.Actor.Role), ServiceActorID: request.Operation.Actor.ServiceActorID, SourceIP: request.Operation.Actor.SourceIP, Action: string(request.Operation.Name), TargetType: "switch", TargetID: request.Operation.DeviceID, DeviceVendor: string(prepared.Device.Vendor), DeviceModel: prepared.Device.Model, DeviceOSVersion: prepared.Device.OSVersion, PluginName: prepared.Metadata.Name, PluginVersion: prepared.Metadata.PluginVersion.String(), RequestPayloadRedacted: auditRequest, CommandPlanRedacted: json.RawMessage(prepared.AuditPlan), Status: string(task.StatusPending), CreatedAt: now})
	if auditErr != nil {
		auditCreated = false
		if request.Operation.Class != operation.ClassQuery || s.config.RequireQueryAudit {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			cancelled, cancelErr := s.tasks.RequestCancel(cleanupCtx, created.ID, s.now().UTC())
			cancel()
			if cancelErr == nil {
				created = cancelled
			}
			return Submission{Task: created, AuditID: created.ID}, apperror.Wrap(apperror.CodeAuditUnavailable, "", auditErr)
		}
		s.reportAuditFailure(ctx, "preflight", auditErr)
	}

	queued, err := s.queue.Queue(ctx, created.ID)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cancelled, cancelErr := s.tasks.RequestCancel(cleanupCtx, created.ID, s.now().UTC())
		if cancelErr == nil {
			created = cancelled
		}
		if auditCreated {
			_, _ = s.audits.Complete(cleanupCtx, created.ID, string(task.StatusCancelled), "", json.RawMessage(`{"queued":false}`), s.now().UTC())
		}
		cancel()
		return Submission{Task: created, AuditID: created.ID}, err
	}

	submission := Submission{Task: queued, AuditID: queued.ID}
	if request.Operation.ExecutionMode == operation.ExecutionModeAsync {
		return submission, nil
	}
	return s.wait(ctx, submission)
}

func (s *Service) replay(ctx context.Context, existing task.Persisted, wantFingerprint string, mode operation.ExecutionMode) (Submission, error) {
	payload, err := decodeTaskPayload(existing)
	if err != nil {
		return Submission{}, apperror.Wrap(apperror.CodeStateConflict, "", err)
	}
	if payload.RequestFingerprint != wantFingerprint {
		return Submission{}, apperror.New(apperror.CodeIdempotencyConflict, "")
	}
	submission := Submission{Task: existing, AuditID: existing.ID, Completed: existing.Status.Terminal(), IdempotentReplay: true}
	if mode == operation.ExecutionModeAsync || submission.Completed {
		return submission, nil
	}
	return s.wait(ctx, submission)
}

func (s *Service) wait(ctx context.Context, submission Submission) (Submission, error) {
	timer := time.NewTimer(s.config.SyncWaitTimeout)
	ticker := time.NewTicker(s.config.WaitPollInterval)
	defer timer.Stop()
	defer ticker.Stop()
	for {
		current, err := s.tasks.Get(ctx, submission.Task.ID)
		if err != nil {
			return Submission{}, err
		}
		submission.Task = current
		if current.Status.Terminal() {
			submission.Completed = true
			return submission, nil
		}
		select {
		case <-ctx.Done():
			return submission, ctx.Err()
		case <-timer.C:
			submission.Deferred = true
			return submission, nil
		case <-ticker.C:
		}
	}
}

func (s *Service) newID() (string, error) {
	id, err := s.ids()
	if err != nil {
		return "", apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	if strings.TrimSpace(id) == "" {
		return "", apperror.New(apperror.CodeInternalError, "")
	}
	return id, nil
}

func (s *Service) reportAuditFailure(ctx context.Context, phase string, err error) {
	if s.reporter != nil {
		s.reporter.ReportAuditFailure(ctx, phase, err)
	}
}

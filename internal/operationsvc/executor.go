package operationsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/concurrency"
	"github.com/dylanLi233/switch-manager/internal/domain/audit"
	"github.com/dylanLi233/switch-manager/internal/domain/operation"
	"github.com/dylanLi233/switch-manager/internal/domain/task"
	"github.com/dylanLi233/switch-manager/internal/scheduler"
	"github.com/dylanLi233/switch-manager/pkg/pluginapi"
)

type commandResultView struct {
	Sequence        int    `json:"sequence"`
	Succeeded       bool   `json:"succeeded"`
	OutputTruncated bool   `json:"output_truncated"`
	ErrorCode       string `json:"error_code,omitempty"`
	DurationMS      int64  `json:"duration_ms"`
}

type pluginResultView struct {
	Status    pluginapi.ResultStatus `json:"status"`
	Data      any                    `json:"data,omitempty"`
	ErrorCode string                 `json:"error_code,omitempty"`
	Commands  []commandResultView    `json:"commands,omitempty"`
}

type saveResultView struct {
	Attempted bool                   `json:"attempted"`
	Status    pluginapi.ResultStatus `json:"status,omitempty"`
	ErrorCode string                 `json:"error_code,omitempty"`
	Commands  []commandResultView    `json:"commands,omitempty"`
}

type storedResult struct {
	DryRun         bool              `json:"dry_run"`
	Plan           *planBundleView   `json:"plan,omitempty"`
	Operation      *pluginResultView `json:"operation,omitempty"`
	SaveConfig     *saveResultView   `json:"save_config,omitempty"`
	AuditCompleted bool              `json:"audit_completed"`
}

// Executor is the scheduler Handler for durable operation tasks.
type Executor struct {
	planner  *Planner
	audits   audit.Repository
	sessions SessionFactory
	guards   *concurrency.Controller
	now      func() time.Time
	reporter AuditFailureReporter
}

func NewExecutor(planner *Planner, audits audit.Repository, sessions SessionFactory, guards *concurrency.Controller) (*Executor, error) {
	if planner == nil {
		return nil, errors.New("operation planner is required")
	}
	if audits == nil {
		return nil, errors.New("audit repository is required")
	}
	if sessions == nil {
		return nil, errors.New("session factory is required")
	}
	if guards == nil {
		return nil, errors.New("concurrency controller is required")
	}
	return &Executor{planner: planner, audits: audits, sessions: sessions, guards: guards, now: time.Now}, nil
}

func (e *Executor) SetAuditFailureReporter(reporter AuditFailureReporter) {
	if e != nil {
		e.reporter = reporter
	}
}

func (e *Executor) Execute(ctx context.Context, value task.Persisted) (execution scheduler.ExecutionResult, returnErr error) {
	if ctx == nil {
		return scheduler.ExecutionResult{}, errors.New("context is required")
	}
	defer func() {
		if returnErr == nil {
			return
		}
		status, code := auditFailureStatus(ctx, returnErr)
		summary, _ := json.Marshal(map[string]any{"status": status, "error_code": code})
		completeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := e.audits.Complete(completeCtx, value.ID, string(status), code, json.RawMessage(summary), e.now().UTC()); err != nil {
			e.reportAuditFailure(completeCtx, "complete_failure", err)
		}
	}()
	payload, err := decodeTaskPayload(value)
	if err != nil {
		return scheduler.ExecutionResult{}, apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	prepared, err := e.planner.prepare(ctx, planInput{Name: payload.Request.Name, Class: payload.Request.Class, DeviceID: payload.Request.DeviceID, Parameters: payload.Request.Parameters, SaveConfig: value.SaveConfig, ConfirmRisk: payload.Request.ConfirmRisk, MainPlanID: payload.MainPlanID, SavePlanID: payload.SavePlanID})
	if err != nil {
		return scheduler.ExecutionResult{}, err
	}
	if prepared.MainHash != payload.MainPlanSHA256 || prepared.SaveHash != payload.SavePlanSHA256 {
		return scheduler.ExecutionResult{}, apperror.New(apperror.CodeStateConflict, "execution plan changed after preflight")
	}

	result := storedResult{DryRun: value.DryRun}
	status := task.StatusSuccess
	errorCode := ""
	if value.DryRun {
		bundle := planBundleView{Main: redactPlan(prepared.MainPlan)}
		if prepared.SavePlan != nil {
			save := redactPlan(*prepared.SavePlan)
			bundle.Save = &save
		}
		result.Plan = &bundle
	} else {
		operationResult, saveResult, executeErr := e.executePrepared(ctx, prepared)
		if executeErr != nil {
			return scheduler.ExecutionResult{}, executeErr
		}
		operationView := viewPluginResult(operationResult)
		result.Operation = &operationView
		status, errorCode = taskStatus(operationResult)
		if saveResult != nil {
			result.SaveConfig = saveResult
			if saveResult.Status != pluginapi.ResultSuccess {
				status = task.StatusPartialSuccess
				errorCode = string(apperror.CodeConfigSaveFailed)
			}
		}
	}

	auditSummary, summaryErr := marshalAuditSummary(result, status, errorCode)
	if summaryErr != nil {
		return scheduler.ExecutionResult{}, apperror.Wrap(apperror.CodeInternalError, "", summaryErr)
	}
	_, auditErr := e.audits.Complete(ctx, value.ID, string(status), errorCode, auditSummary, e.now().UTC())
	if auditErr == nil {
		result.AuditCompleted = true
	} else {
		e.reportAuditFailure(ctx, "complete", auditErr)
		if payload.Request.Class != operation.ClassQuery && status == task.StatusSuccess {
			status = task.StatusPartialSuccess
			errorCode = string(apperror.CodeAuditUnavailable)
		}
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return scheduler.ExecutionResult{}, apperror.Wrap(apperror.CodeInternalError, "", err)
	}
	return scheduler.ExecutionResult{Status: status, Result: json.RawMessage(encoded), ErrorCode: errorCode, PluginName: prepared.Metadata.Name, PluginVersion: prepared.Metadata.PluginVersion.String()}, nil
}

func (e *Executor) executePrepared(ctx context.Context, prepared preparedOperation) (pluginapi.OperationResult, *saveResultView, error) {
	var mainResult pluginapi.OperationResult
	var saveView *saveResultView
	err := e.guards.Do(ctx, prepared.Device.ID, func(guarded context.Context) error {
		session, err := e.sessions.Open(guarded, prepared.Device)
		if err != nil {
			var app *apperror.Error
			if errors.As(err, &app) {
				return err
			}
			return apperror.Wrap(apperror.CodeDeviceUnreachable, "", err)
		}
		defer session.Close()

		mainResult, err = executeAndParse(guarded, e.now, prepared.Plugin, session, prepared.MainPlan)
		if err != nil {
			return err
		}
		if prepared.SavePlan == nil || mainResult.Status != pluginapi.ResultSuccess {
			return nil
		}
		saveView = &saveResultView{Attempted: true}
		saveResult, saveErr := executeAndParse(guarded, e.now, prepared.Plugin, session, *prepared.SavePlan)
		if saveErr != nil {
			if errors.Is(saveErr, context.Canceled) || errors.Is(saveErr, context.DeadlineExceeded) {
				return saveErr
			}
			saveView.Status = pluginapi.ResultFailed
			saveView.ErrorCode = string(apperror.CodeConfigSaveFailed)
			return nil
		}
		saveView.Status = saveResult.Status
		saveView.Commands = viewCommands(saveResult.Commands)
		if saveResult.Status != pluginapi.ResultSuccess {
			saveView.ErrorCode = string(apperror.CodeConfigSaveFailed)
		}
		return nil
	})
	if err != nil {
		return pluginapi.OperationResult{}, nil, err
	}
	return mainResult, saveView, nil
}

func executeAndParse(ctx context.Context, now func() time.Time, plugin pluginapi.Plugin, session pluginapi.CLISession, plan pluginapi.ExecutionPlan) (pluginapi.OperationResult, error) {
	transcript, executeErr := executePlan(ctx, now, session, plan)
	if executeErr != nil && (errors.Is(executeErr, context.Canceled) || errors.Is(executeErr, context.DeadlineExceeded)) {
		return pluginapi.OperationResult{}, executeErr
	}
	parsed, err := plugin.ParseResult(ctx, plan, transcript)
	if err != nil {
		return pluginapi.OperationResult{}, apperror.Wrap(apperror.CodeCommandOutputUnparsable, "", err)
	}
	if err := parsed.Validate(); err != nil {
		return pluginapi.OperationResult{}, apperror.Wrap(apperror.CodeCommandOutputUnparsable, "", err)
	}
	return parsed, nil
}

func executePlan(ctx context.Context, now func() time.Time, session pluginapi.CLISession, plan pluginapi.ExecutionPlan) (pluginapi.Transcript, error) {
	started := now().UTC()
	records := make([]pluginapi.CommandRecord, 0, len(plan.Commands))
	var executeErr error
	for _, command := range plan.Commands {
		commandStarted := now().UTC()
		output, err := session.Execute(ctx, command)
		duration := output.Duration
		if duration <= 0 {
			duration = now().UTC().Sub(commandStarted)
			if duration < 0 {
				duration = 0
			}
		}
		code := ""
		if err != nil {
			code = commandErrorCode(err)
		}
		records = append(records, pluginapi.CommandRecord{Sequence: command.Sequence, Command: command.Text, Output: output.Output, Succeeded: err == nil, ErrorCode: code, Duration: duration, OutputTruncated: output.OutputTruncated})
		if err != nil {
			executeErr = err
			break
		}
	}
	finished := now().UTC()
	if finished.Before(started) {
		finished = started
	}
	return pluginapi.Transcript{StartedAt: started, FinishedAt: finished, Commands: records}, executeErr
}

func commandErrorCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return string(apperror.CodeCommandTimeout)
	case errors.Is(err, context.Canceled):
		return string(apperror.CodeCommandRejected)
	}
	var app *apperror.Error
	if errors.As(err, &app) {
		return string(app.Code)
	}
	return string(apperror.CodeCommandRejected)
}

func taskStatus(result pluginapi.OperationResult) (task.Status, string) {
	switch result.Status {
	case pluginapi.ResultSuccess:
		return task.StatusSuccess, ""
	case pluginapi.ResultPartialSuccess:
		return task.StatusPartialSuccess, result.ErrorCode
	default:
		return task.StatusFailed, result.ErrorCode
	}
}

func viewPluginResult(result pluginapi.OperationResult) pluginResultView {
	return pluginResultView{Status: result.Status, Data: result.Data, ErrorCode: result.ErrorCode, Commands: viewCommands(result.Commands)}
}

func viewCommands(commands []pluginapi.CommandExecution) []commandResultView {
	result := make([]commandResultView, len(commands))
	for index, command := range commands {
		result[index] = commandResultView{Sequence: command.Sequence, Succeeded: command.Succeeded, OutputTruncated: command.OutputTruncated, ErrorCode: command.ErrorCode, DurationMS: command.Duration.Milliseconds()}
	}
	return result
}

func marshalAuditSummary(result storedResult, status task.Status, errorCode string) (json.RawMessage, error) {
	summary := map[string]any{"dry_run": result.DryRun, "status": status, "error_code": errorCode}
	if result.Operation != nil {
		summary["operation_status"] = result.Operation.Status
		summary["operation_error_code"] = result.Operation.ErrorCode
		summary["operation_command_count"] = len(result.Operation.Commands)
	}
	if result.SaveConfig != nil {
		summary["save_attempted"] = result.SaveConfig.Attempted
		summary["save_status"] = result.SaveConfig.Status
		summary["save_error_code"] = result.SaveConfig.ErrorCode
	}
	data, err := json.Marshal(summary)
	if err != nil {
		return nil, fmt.Errorf("marshal audit summary: %w", err)
	}
	return json.RawMessage(data), nil
}

func (e *Executor) reportAuditFailure(ctx context.Context, phase string, err error) {
	if e.reporter != nil {
		e.reporter.ReportAuditFailure(ctx, phase, err)
	}
}

func auditFailureStatus(ctx context.Context, err error) (task.Status, string) {
	if errors.Is(context.Cause(ctx), scheduler.ErrTaskCancelled) {
		return task.StatusCancelled, ""
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return task.StatusInterrupted, ""
	}
	app := apperror.Normalize(err)
	return task.StatusFailed, string(app.Code)
}

var _ scheduler.Handler = (*Executor)(nil)

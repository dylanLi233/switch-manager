package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dylanLi233/switch-manager/internal/apperror"
	"github.com/dylanLi233/switch-manager/internal/domain/audit"
)

const auditColumns = `
	id::text, request_id, COALESCE(task_id::text,''), actor_user_id::text,
	actor_username, actor_role, COALESCE(service_actor_id,''),
	COALESCE(host(source_ip),''), action, target_type, target_id,
	COALESCE(device_vendor,''), COALESCE(device_model,''),
	COALESCE(device_os_version,''), COALESCE(plugin_name,''),
	COALESCE(plugin_version,''), request_payload_redacted,
	command_plan_redacted, result_summary_redacted, status,
	COALESCE(error_code,''), created_at, finished_at`

// AuditRepository persists redacted audit records.
type AuditRepository struct{ q DBTX }

// Create inserts the pending or completed audit record.
func (r *AuditRepository) Create(ctx context.Context, value audit.Record) (audit.Record, error) {
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now().UTC()
	}
	if err := value.Validate(); err != nil {
		return audit.Record{}, apperror.Wrap(apperror.CodeValidationError, "", err)
	}
	row := r.q.QueryRow(ctx, `
		INSERT INTO audit_logs (
			id, request_id, task_id, actor_user_id, actor_username, actor_role,
			service_actor_id, source_ip, action, target_type, target_id,
			device_vendor, device_model, device_os_version, plugin_name,
			plugin_version, request_payload_redacted, command_plan_redacted,
			result_summary_redacted, status, error_code, created_at, finished_at
		) VALUES (
			$1::uuid,$2,$3::uuid,$4::uuid,$5,$6,$7,NULLIF($8,'')::inet,$9,$10,$11,
			$12,$13,$14,$15,$16,$17::jsonb,$18::jsonb,$19::jsonb,$20,$21,$22,$23
		) RETURNING `+auditColumns,
		value.ID, value.RequestID, nilIfBlank(value.TaskID), value.ActorUserID,
		value.ActorUsername, value.ActorRole, nilIfBlank(value.ServiceActorID),
		value.SourceIP, value.Action, value.TargetType, value.TargetID,
		nilIfBlank(value.DeviceVendor), nilIfBlank(value.DeviceModel),
		nilIfBlank(value.DeviceOSVersion), nilIfBlank(value.PluginName),
		nilIfBlank(value.PluginVersion), bytesOrNil(value.RequestPayloadRedacted),
		bytesOrNil(value.CommandPlanRedacted), bytesOrNil(value.ResultSummaryRedacted),
		value.Status, nilIfBlank(value.ErrorCode), value.CreatedAt, value.FinishedAt,
	)
	result, err := scanAudit(row)
	return result, mapDatabaseError(err, apperror.CodeResourceNotFound, "create audit")
}

// Get returns an audit record by ID.
func (r *AuditRepository) Get(ctx context.Context, id string) (audit.Record, error) {
	row := r.q.QueryRow(ctx, `SELECT `+auditColumns+` FROM audit_logs WHERE id=$1::uuid`, id)
	result, err := scanAudit(row)
	return result, mapDatabaseError(err, apperror.CodeResourceNotFound, "get audit")
}

// Complete writes final redacted result metadata.
func (r *AuditRepository) Complete(
	ctx context.Context,
	id string,
	status string,
	errorCode string,
	result json.RawMessage,
	finishedAt time.Time,
) (audit.Record, error) {
	if finishedAt.IsZero() {
		return audit.Record{}, apperror.Wrap(apperror.CodeValidationError, "", fmt.Errorf("audit finish time is required"))
	}
	row := r.q.QueryRow(ctx, `
		UPDATE audit_logs SET status=$2, error_code=$3,
			result_summary_redacted=$4::jsonb, finished_at=$5
		WHERE id=$1::uuid
		RETURNING `+auditColumns,
		id, status, nilIfBlank(errorCode), bytesOrNil(result), finishedAt,
	)
	value, err := scanAudit(row)
	return value, mapDatabaseError(err, apperror.CodeResourceNotFound, "complete audit")
}

func scanAudit(row rowScanner) (audit.Record, error) {
	var result audit.Record
	var requestPayload, commandPlan, resultSummary []byte
	var finishedAt sql.NullTime
	if err := row.Scan(
		&result.ID, &result.RequestID, &result.TaskID, &result.ActorUserID,
		&result.ActorUsername, &result.ActorRole, &result.ServiceActorID,
		&result.SourceIP, &result.Action, &result.TargetType, &result.TargetID,
		&result.DeviceVendor, &result.DeviceModel, &result.DeviceOSVersion,
		&result.PluginName, &result.PluginVersion, &requestPayload, &commandPlan,
		&resultSummary, &result.Status, &result.ErrorCode, &result.CreatedAt,
		&finishedAt,
	); err != nil {
		return audit.Record{}, err
	}
	result.RequestPayloadRedacted = append(json.RawMessage(nil), requestPayload...)
	result.CommandPlanRedacted = append(json.RawMessage(nil), commandPlan...)
	result.ResultSummaryRedacted = append(json.RawMessage(nil), resultSummary...)
	result.FinishedAt = timePointer(finishedAt)
	if err := result.Validate(); err != nil {
		return audit.Record{}, fmt.Errorf("invalid audit row: %w", err)
	}
	return result, nil
}

var _ audit.Repository = (*AuditRepository)(nil)

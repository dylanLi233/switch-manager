package apperror

import "net/http"

// Code is a stable machine-readable error identifier.
type Code string

// Definition is the canonical transport metadata for an error code.
type Definition struct {
	HTTPStatus     int
	Retryable      bool
	DefaultMessage string
}

const (
	CodeValidationError           Code = "VALIDATION_ERROR"
	CodeUnauthorized              Code = "UNAUTHORIZED"
	CodeForbidden                 Code = "FORBIDDEN"
	CodeResourceNotFound          Code = "RESOURCE_NOT_FOUND"
	CodeDeviceNotFound            Code = "DEVICE_NOT_FOUND"
	CodeCredentialNotFound        Code = "CREDENTIAL_NOT_FOUND"
	CodeTaskNotFound              Code = "TASK_NOT_FOUND"
	CodeBackupNotFound            Code = "BACKUP_NOT_FOUND"
	CodeIdempotencyConflict       Code = "IDEMPOTENCY_CONFLICT"
	CodeStateConflict             Code = "STATE_CONFLICT"
	CodeDeviceDisabled            Code = "DEVICE_DISABLED"
	CodeIdentityMismatch          Code = "IDENTITY_MISMATCH"
	CodeDeviceBusy                Code = "DEVICE_BUSY"
	CodeTaskNotCancellable        Code = "TASK_NOT_CANCELLABLE"
	CodePluginNotFound            Code = "PLUGIN_NOT_FOUND"
	CodePluginIncompatible        Code = "PLUGIN_INCOMPATIBLE"
	CodeUnsupportedOperation      Code = "UNSUPPORTED_OPERATION"
	CodeCapabilityNotSupported    Code = "CAPABILITY_NOT_SUPPORTED"
	CodeOperationNotImplemented   Code = "OPERATION_NOT_IMPLEMENTED"
	CodeRiskConfirmationRequired  Code = "RISK_CONFIRMATION_REQUIRED"
	CodeDangerousCommandBlocked   Code = "DANGEROUS_COMMAND_BLOCKED"
	CodeDeviceUnreachable         Code = "DEVICE_UNREACHABLE"
	CodeAuthenticationFailed      Code = "AUTHENTICATION_FAILED"
	CodeHostKeyVerificationFailed Code = "HOST_KEY_VERIFICATION_FAILED"
	CodeSSHTimeout                Code = "SSH_TIMEOUT"
	CodeCommandTimeout            Code = "COMMAND_TIMEOUT"
	CodeCommandRejected           Code = "COMMAND_REJECTED"
	CodeCommandOutputUnparsable   Code = "COMMAND_OUTPUT_UNPARSABLE"
	CodeResultTooLarge            Code = "RESULT_TOO_LARGE"
	CodeConfigSaveFailed          Code = "CONFIG_SAVE_FAILED"
	CodeBackupFailed              Code = "BACKUP_FAILED"
	CodeBackupIncompatible        Code = "BACKUP_INCOMPATIBLE"
	CodeBackupIntegrityFailed     Code = "BACKUP_INTEGRITY_FAILED"
	CodeRestoreFailed             Code = "RESTORE_FAILED"
	CodeAuditUnavailable          Code = "AUDIT_UNAVAILABLE"
	CodeDatabaseUnavailable       Code = "DATABASE_UNAVAILABLE"
	CodeInternalError             Code = "INTERNAL_ERROR"
)

var definitions = map[Code]Definition{
	CodeValidationError:           {http.StatusBadRequest, false, "request validation failed"},
	CodeUnauthorized:              {http.StatusUnauthorized, false, "authentication required"},
	CodeForbidden:                 {http.StatusForbidden, false, "permission denied"},
	CodeResourceNotFound:          {http.StatusNotFound, false, "resource not found"},
	CodeDeviceNotFound:            {http.StatusNotFound, false, "device not found"},
	CodeCredentialNotFound:        {http.StatusNotFound, false, "credential not found"},
	CodeTaskNotFound:              {http.StatusNotFound, false, "task not found"},
	CodeBackupNotFound:            {http.StatusNotFound, false, "backup not found"},
	CodeIdempotencyConflict:       {http.StatusConflict, false, "idempotency key conflicts with an existing request"},
	CodeStateConflict:             {http.StatusConflict, false, "resource state conflicts with the requested operation"},
	CodeDeviceDisabled:            {http.StatusConflict, false, "device is disabled"},
	CodeIdentityMismatch:          {http.StatusConflict, false, "device identity does not match its registration"},
	CodeDeviceBusy:                {http.StatusLocked, true, "device is busy"},
	CodeTaskNotCancellable:        {http.StatusConflict, false, "task cannot be cancelled in its current state"},
	CodePluginNotFound:            {http.StatusUnprocessableEntity, false, "device plugin not found"},
	CodePluginIncompatible:        {http.StatusUnprocessableEntity, false, "device plugin is incompatible"},
	CodeUnsupportedOperation:      {http.StatusUnprocessableEntity, false, "operation is not supported"},
	CodeCapabilityNotSupported:    {http.StatusUnprocessableEntity, false, "device capability is not supported"},
	CodeOperationNotImplemented:   {http.StatusNotImplemented, false, "operation is not implemented"},
	CodeRiskConfirmationRequired:  {http.StatusUnprocessableEntity, false, "risk confirmation is required"},
	CodeDangerousCommandBlocked:   {http.StatusForbidden, false, "dangerous command is blocked"},
	CodeDeviceUnreachable:         {http.StatusServiceUnavailable, true, "device is unreachable"},
	CodeAuthenticationFailed:      {http.StatusBadGateway, false, "device authentication failed"},
	CodeHostKeyVerificationFailed: {http.StatusBadGateway, false, "SSH host key verification failed"},
	CodeSSHTimeout:                {http.StatusGatewayTimeout, true, "SSH operation timed out"},
	CodeCommandTimeout:            {http.StatusGatewayTimeout, false, "device command timed out"},
	CodeCommandRejected:           {http.StatusUnprocessableEntity, false, "device rejected the command"},
	CodeCommandOutputUnparsable:   {http.StatusBadGateway, false, "device output could not be parsed"},
	CodeResultTooLarge:            {http.StatusUnprocessableEntity, false, "device result exceeds the configured limit"},
	CodeConfigSaveFailed:          {http.StatusBadGateway, false, "device configuration could not be saved"},
	CodeBackupFailed:              {http.StatusBadGateway, false, "configuration backup failed"},
	CodeBackupIncompatible:        {http.StatusUnprocessableEntity, false, "backup is incompatible with the target device"},
	CodeBackupIntegrityFailed:     {http.StatusUnprocessableEntity, false, "backup integrity validation failed"},
	CodeRestoreFailed:             {http.StatusBadGateway, false, "configuration restore failed"},
	CodeAuditUnavailable:          {http.StatusServiceUnavailable, true, "audit service is unavailable"},
	CodeDatabaseUnavailable:       {http.StatusServiceUnavailable, true, "database is unavailable"},
	CodeInternalError:             {http.StatusInternalServerError, false, "internal server error"},
}

// Lookup returns the canonical definition for code.
func Lookup(code Code) (Definition, bool) {
	definition, ok := definitions[code]
	return definition, ok
}

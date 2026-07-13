package pluginapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Plugin is implemented by statically compiled vendor adapters.
// It cannot create sessions, persist data, create tasks, or write audits.
type Plugin interface {
	Metadata() Metadata
	Detect(context.Context, CLISession) (DeviceInfo, error)
	Capabilities(context.Context, DeviceInfo) (CapabilitySet, error)
	BuildPlan(context.Context, PlanRequest) (ExecutionPlan, error)
	ParseResult(context.Context, ExecutionPlan, Transcript) (OperationResult, error)
}

type ErrorCode string

const (
	ErrorInvalidRequest       ErrorCode = "INVALID_REQUEST"
	ErrorDetectionFailed      ErrorCode = "DETECTION_FAILED"
	ErrorUnsupportedOperation ErrorCode = "UNSUPPORTED_OPERATION"
	ErrorPlanInvalid          ErrorCode = "PLAN_INVALID"
	ErrorOutputUnparsable     ErrorCode = "OUTPUT_UNPARSABLE"
	ErrorResultTooLarge       ErrorCode = "RESULT_TOO_LARGE"
)

type Error struct {
	Code    ErrorCode
	Message string
	cause   error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if strings.TrimSpace(e.Message) == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func NewError(code ErrorCode, message string) error {
	return WrapError(code, message, nil)
}

func WrapError(code ErrorCode, message string, cause error) error {
	switch code {
	case ErrorInvalidRequest, ErrorDetectionFailed, ErrorUnsupportedOperation, ErrorPlanInvalid, ErrorOutputUnparsable, ErrorResultTooLarge:
	default:
		code = ErrorPlanInvalid
		message = "plugin returned an invalid error code"
	}
	return &Error{Code: code, Message: strings.TrimSpace(message), cause: cause}
}

func IsErrorCode(err error, code ErrorCode) bool {
	var target *Error
	return errors.As(err, &target) && target.Code == code
}

func ValidatePlugin(plugin Plugin, runtime Version) error {
	if IsNilPlugin(plugin) {
		return errors.New("plugin is nil")
	}
	if err := plugin.Metadata().Validate(runtime); err != nil {
		return fmt.Errorf("validate plugin metadata: %w", err)
	}
	return nil
}

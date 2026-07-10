// Package apperror provides stable application error codes and safe public metadata.
package apperror

import (
	"errors"
	"fmt"
	"strings"
)

// Error is a stable application error. Cause is intentionally private so it
// cannot be serialized into an API response by accident.
type Error struct {
	Code       Code
	Message    string
	HTTPStatus int
	Retryable  bool
	Details    any
	cause      error
}

// New creates an application error without an underlying cause.
func New(code Code, message string) *Error { return build(code, message, nil) }

// Wrap creates an application error while retaining an internal cause for logs
// and errors.Is/errors.As. The public message must not contain secrets.
func Wrap(code Code, message string, cause error) *Error { return build(code, message, cause) }

func build(code Code, message string, cause error) *Error {
	def, ok := Lookup(code)
	if !ok {
		unknownCause := fmt.Errorf("unknown application error code %q", code)
		if cause != nil {
			unknownCause = errors.Join(unknownCause, cause)
		}
		internal := definitions[CodeInternalError]
		return &Error{
			Code:       CodeInternalError,
			Message:    internal.DefaultMessage,
			HTTPStatus: internal.HTTPStatus,
			Retryable:  internal.Retryable,
			cause:      unknownCause,
		}
	}
	if strings.TrimSpace(message) == "" || code == CodeInternalError {
		message = def.DefaultMessage
	}
	return &Error{
		Code:       code,
		Message:    message,
		HTTPStatus: def.HTTPStatus,
		Retryable:  def.Retryable,
		cause:      cause,
	}
}

// Error returns only safe public metadata and never includes the wrapped cause.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the internal cause to trusted Go error inspection.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// WithDetails returns a shallow copy containing public structured details.
func (e *Error) WithDetails(details any) *Error {
	if e == nil {
		return nil
	}
	clone := *e
	clone.Details = details
	return &clone
}

// WithRetryable returns a shallow copy with an instance-specific retry hint.
func (e *Error) WithRetryable(retryable bool) *Error {
	if e == nil {
		return nil
	}
	clone := *e
	clone.Retryable = retryable
	return &clone
}

// Normalize converts arbitrary errors into a safe application error and
// restores the canonical HTTP status for known codes.
func Normalize(err error) *Error {
	if err == nil {
		return New(CodeInternalError, "")
	}
	var app *Error
	if errors.As(err, &app) {
		def, ok := Lookup(app.Code)
		if !ok {
			return Wrap(CodeInternalError, "", err)
		}
		clone := *app
		clone.HTTPStatus = def.HTTPStatus
		if strings.TrimSpace(clone.Message) == "" || clone.Code == CodeInternalError {
			clone.Message = def.DefaultMessage
		}
		if clone.Code == CodeInternalError {
			clone.Details = nil
		}
		return &clone
	}
	return Wrap(CodeInternalError, "", err)
}

// IsCode reports whether err contains an application error with the given code.
func IsCode(err error, code Code) bool {
	var app *Error
	return errors.As(err, &app) && app.Code == code
}

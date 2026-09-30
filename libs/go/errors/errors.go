package errors

import (
	"errors"
	"fmt"
)

// ErrorCode is a machine-readable error classification. The values match the
// codes declared in //proto/common:common.proto so that an error can cross a
// service boundary unchanged.
type ErrorCode string

const (
	// ErrCodeUnknown represents an unclassified error.
	ErrCodeUnknown ErrorCode = "UNKNOWN"
	// ErrCodeNotFound represents a missing resource.
	ErrCodeNotFound ErrorCode = "NOT_FOUND"
	// ErrCodeInvalidArgument represents a malformed or invalid request.
	ErrCodeInvalidArgument ErrorCode = "INVALID_ARGUMENT"
	// ErrCodePermissionDenied represents an authorization failure.
	ErrCodePermissionDenied ErrorCode = "PERMISSION_DENIED"
	// ErrCodeInternal represents an unexpected server-side failure.
	ErrCodeInternal ErrorCode = "INTERNAL"
	// ErrCodeUnavailable represents a dependency that is temporarily down.
	ErrCodeUnavailable ErrorCode = "UNAVAILABLE"
)

// AppError is the repository-wide error type. It carries a machine-readable
// code, a human-readable message and an optional wrapped cause.
type AppError struct {
	Code    ErrorCode
	Message string
	Err     error
}

// Error implements the error interface.
func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap returns the underlying error so that errors.Is/errors.As keep working
// through a wrap chain.
func (e *AppError) Unwrap() error {
	return e.Err
}

// New creates a new AppError.
func New(code ErrorCode, message string) *AppError {
	return &AppError{Code: code, Message: message}
}

// Wrap wraps an existing error with a code and message. A nil err yields a
// plain AppError rather than an error that reports "<nil>" when printed.
func Wrap(err error, code ErrorCode, message string) *AppError {
	if err == nil {
		return nil
	}
	return &AppError{Code: code, Message: message, Err: err}
}

// Is reports whether any error in err's chain carries the given code.
func Is(err error, code ErrorCode) bool {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Code == code
	}
	return false
}

// Code returns the code of the first AppError in err's chain, and whether one
// was found.
func Code(err error) (ErrorCode, bool) {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Code, true
	}
	return ErrCodeUnknown, false
}

// Message returns the message of the first AppError in err's chain, and whether
// one was found.
func Message(err error) (string, bool) {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Message, true
	}
	return "", false
}

// As extracts the first AppError from err's chain.
//
// Prefer the typed helpers (Is, Code, Message) in new code: this name shadows
// the standard library's errors.As, which makes call sites easy to misread.
func As(err error) (*AppError, bool) {
	var appErr *AppError
	ok := errors.As(err, &appErr)
	return appErr, ok
}

// NotFound creates a NOT_FOUND error.
func NotFound(message string) *AppError {
	return New(ErrCodeNotFound, message)
}

// InvalidArgument creates an INVALID_ARGUMENT error.
func InvalidArgument(message string) *AppError {
	return New(ErrCodeInvalidArgument, message)
}

// PermissionDenied creates a PERMISSION_DENIED error.
func PermissionDenied(message string) *AppError {
	return New(ErrCodePermissionDenied, message)
}

// Internal creates an INTERNAL error.
func Internal(message string) *AppError {
	return New(ErrCodeInternal, message)
}

// Unavailable creates an UNAVAILABLE error.
func Unavailable(message string) *AppError {
	return New(ErrCodeUnavailable, message)
}

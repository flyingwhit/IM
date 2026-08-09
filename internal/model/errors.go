package model

import "errors"

// Domain errors — used across all layers to signal specific business failures.
// Each error wraps a sentinel so callers can use errors.Is to check.
var (
	ErrNotFound     = errors.New("resource not found")
	ErrDuplicate    = errors.New("resource already exists")
	ErrInvalidInput = errors.New("invalid input")
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrTokenExpired = errors.New("token expired")
	ErrConflict     = errors.New("conflict") // e.g. already friends
)

// AppError wraps a sentinel error with a user-facing message.
type AppError struct {
	Err     error  // sentinel (use errors.Is)
	Message string // human-readable
}

func (e *AppError) Error() string {
	return e.Message
}

func (e *AppError) Unwrap() error {
	return e.Err
}

// NewAppError creates a new AppError.
func NewAppError(err error, msg string) *AppError {
	return &AppError{Err: err, Message: msg}
}

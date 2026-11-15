package domain

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNotFound                 = errors.New("resource not found")
	ErrConflict                 = errors.New("conflict with existing resource or stale version")
	ErrOverlap                  = errors.New("date range overlaps an existing active inventory claim")
	ErrReleaseRestricted        = errors.New("dates are not yet released for the requested booking channel")
	ErrValidation               = errors.New("validation failed")
	ErrUnauthorized             = errors.New("authentication required or invalid credentials")
	ErrForbidden                = errors.New("insufficient permissions")
	ErrPasswordRotationRequired = errors.New("password rotation required before proceeding")
	ErrRateLimited              = errors.New("too many attempts; please try again later")
	ErrStripeDisabled           = errors.New("online card payments are disabled for this property")
	ErrActiveCheckoutExists     = errors.New("an active checkout session already exists for this booking hold")
)

// ValidationError carries field-level validation issues without leaking internals.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	if len(e.Fields) == 0 {
		return ErrValidation.Error()
	}
	parts := make([]string, 0, len(e.Fields))
	for k, v := range e.Fields {
		parts = append(parts, fmt.Sprintf("%s: %s", k, v))
	}
	return fmt.Sprintf("%s (%s)", ErrValidation.Error(), strings.Join(parts, "; "))
}

func (e *ValidationError) Unwrap() error {
	return ErrValidation
}

// NewValidationError builds a wrapped ValidationError if fields are non-empty.
func NewValidationError(fields map[string]string) error {
	if len(fields) == 0 {
		return nil
	}
	return &ValidationError{Fields: fields}
}

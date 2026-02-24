package horos

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ServiceError is a structured error that travels across the wire.
// Unlike Go's error interface (a string), ServiceError carries a machine-readable
// code, a human message, and optional typed details — all serializable.
//
// ServiceError implements Codec[ServiceError] so it can be encoded in the
// same wire envelope as any other payload.
type ServiceError struct {
	// Code is a machine-readable error code (e.g. "NOT_FOUND", "RATE_LIMITED").
	Code string `json:"code"`

	// Message is a human-readable description.
	Message string `json:"message"`

	// Details is optional structured data for the error (retry-after, field
	// validation errors, etc.). It is codec-dependent: JSON for now.
	Details json.RawMessage `json:"details,omitempty"`

	// Service is the originating service name (set by the handler).
	Service string `json:"service,omitempty"`
}

// Error implements the error interface.
func (e *ServiceError) Error() string {
	if e.Service != "" {
		return fmt.Sprintf("%s: [%s] %s", e.Service, e.Code, e.Message)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Is supports errors.Is matching by code.
func (e *ServiceError) Is(target error) bool {
	var se *ServiceError
	if errors.As(target, &se) {
		return e.Code == se.Code
	}
	return false
}

// serviceErrorEnvelope is the on-wire JSON shape. The "__error" key is a
// sentinel that lets DetectError distinguish error payloads from normal ones.
type serviceErrorEnvelope struct {
	Error ServiceError `json:"__error"`
}

// Encode serializes the ServiceError to JSON with the __error sentinel.
func (e ServiceError) Encode() ([]byte, error) {
	return json.Marshal(serviceErrorEnvelope{Error: e})
}

// Decode deserializes a ServiceError from JSON.
func (e ServiceError) Decode(data []byte) (ServiceError, error) {
	var env serviceErrorEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return ServiceError{}, fmt.Errorf("horos: decode service error: %w", err)
	}
	return env.Error, nil
}

// DetectError checks if a payload contains a ServiceError (by looking for
// the __error sentinel). Returns the error and true if found.
func DetectError(payload []byte) (*ServiceError, bool) {
	// Quick check: avoid full unmarshal if sentinel is absent.
	if len(payload) < 12 { // minimum: {"__error":{}}
		return nil, false
	}

	var env serviceErrorEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, false
	}
	if env.Error.Code == "" {
		return nil, false
	}
	return &env.Error, true
}

// ToServiceError converts any error to a ServiceError. If the error is already
// a *ServiceError, it is returned as-is. Otherwise, a generic INTERNAL error
// is created.
func ToServiceError(err error) *ServiceError {
	var se *ServiceError
	if errors.As(err, &se) {
		return se
	}
	return &ServiceError{
		Code:    "INTERNAL",
		Message: err.Error(),
	}
}

// Common error codes as pre-built ServiceError values for use with errors.Is.
var (
	ErrNotFound    = &ServiceError{Code: "NOT_FOUND"}
	ErrBadRequest  = &ServiceError{Code: "BAD_REQUEST"}
	ErrInternal    = &ServiceError{Code: "INTERNAL"}
	ErrRateLimited = &ServiceError{Code: "RATE_LIMITED"}
	ErrForbidden   = &ServiceError{Code: "FORBIDDEN"}
	ErrConflict    = &ServiceError{Code: "CONFLICT"}
)

// NewServiceError creates a ServiceError with the given code and message.
func NewServiceError(code, message string) *ServiceError {
	return &ServiceError{Code: code, Message: message}
}

// WithDetails returns a copy of the error with JSON details attached.
// Returns an error if v cannot be marshaled to JSON.
func (e *ServiceError) WithDetails(v any) (*ServiceError, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("horos: marshal details: %w", err)
	}
	cp := *e
	cp.Details = data
	return &cp, nil
}

// WithService returns a copy of the error with the service name set.
func (e *ServiceError) WithService(service string) *ServiceError {
	cp := *e
	cp.Service = service
	return &cp
}

// ErrEncode is returned when request/response encoding fails.
type ErrEncode struct {
	Service string
	Cause   error
}

func (e *ErrEncode) Error() string {
	return fmt.Sprintf("horos: encode failed for %s: %v", e.Service, e.Cause)
}

func (e *ErrEncode) Unwrap() error { return e.Cause }

// ErrDecode is returned when request/response decoding fails.
type ErrDecode struct {
	Service string
	Cause   error
}

func (e *ErrDecode) Error() string {
	return fmt.Sprintf("horos: decode failed for %s: %v", e.Service, e.Cause)
}

func (e *ErrDecode) Unwrap() error { return e.Cause }

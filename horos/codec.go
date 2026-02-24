// Package horos provides the HOROS type system: typed service contracts,
// codec-agnostic serialization, structured errors, and a wire envelope format
// for inter-service communication.
//
// The type system sits above connectivity.Handler (bytes in, bytes out) and
// provides compile-time safety for what travels inside those bytes.
package horos

import "context"

// Codec defines how a type serializes itself to and from bytes.
// The F-bounded constraint T Codec[T] ensures that Decode returns the
// concrete type, not an interface — zero runtime casts, zero panics.
//
// This requires Go 1.26+ which lifted the restriction on self-referential
// type parameter lists.
type Codec[T any] interface {
	Encode() ([]byte, error)
	Decode([]byte) (T, error)
}

// Encoder writes a value to bytes. This is the write-only side of Codec,
// useful when a function only needs to serialize (e.g. building a request).
type Encoder interface {
	Encode() ([]byte, error)
}

// Contract is a typed service contract: it binds a request type and a response
// type together with a service name. Both Req and Resp must be self-codecs.
//
// A Contract is metadata — it carries no state and exists to give the compiler
// enough information to type-check service calls at build time.
type Contract[Req Codec[Req], Resp Codec[Resp]] struct {
	// Service is the name used for routing in connectivity.Router.
	Service string

	// FormatID identifies the wire format (1=JSON, 2=msgpack).
	// When zero, the registry default is used.
	FormatID uint16
}

// NewContract creates a contract for a service with the default wire format.
func NewContract[Req Codec[Req], Resp Codec[Resp]](service string) Contract[Req, Resp] {
	return Contract[Req, Resp]{Service: service}
}

// WithFormat returns a copy of the contract using the specified format ID.
func (c Contract[Req, Resp]) WithFormat(id uint16) Contract[Req, Resp] {
	c.FormatID = id
	return c
}

// Call encodes req, dispatches via the provided caller function, and decodes
// the response. The caller is typically connectivity.Router.Call or a test stub.
//
// This is the typed entry point: callers get compile-time guarantees on both
// the request and response types.
func (c Contract[Req, Resp]) Call(
	ctx context.Context,
	caller func(ctx context.Context, service string, payload []byte) ([]byte, error),
	req Req,
) (Resp, error) {
	var zero Resp

	payload, err := req.Encode()
	if err != nil {
		return zero, &ErrEncode{Service: c.Service, Cause: err}
	}

	envelope, err := Wrap(c.FormatID, payload)
	if err != nil {
		return zero, &ErrEncode{Service: c.Service, Cause: err}
	}

	raw, err := caller(ctx, c.Service, envelope)
	if err != nil {
		return zero, err // preserve caller's error types (circuit open, timeout, etc.)
	}

	_, respPayload, err := Unwrap(raw)
	if err != nil {
		return zero, &ErrDecode{Service: c.Service, Cause: err}
	}

	// Check for ServiceError in the response payload.
	if svcErr, ok := DetectError(respPayload); ok {
		return zero, svcErr
	}

	return zero.Decode(respPayload)
}

// Handler returns a connectivity-compatible handler (bytes in, bytes out)
// from a typed endpoint function. This is the server side of a contract.
func (c Contract[Req, Resp]) Handler(
	fn func(ctx context.Context, req Req) (Resp, error),
) func(ctx context.Context, payload []byte) ([]byte, error) {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		var req Req

		_, reqPayload, err := Unwrap(payload)
		if err != nil {
			return nil, &ErrDecode{Service: c.Service, Cause: err}
		}

		req, err = req.Decode(reqPayload)
		if err != nil {
			return nil, &ErrDecode{Service: c.Service, Cause: err}
		}

		resp, err := fn(ctx, req)
		if err != nil {
			// Wrap application errors as ServiceError in the envelope.
			svcErr := ToServiceError(err)
			errPayload, encErr := svcErr.Encode()
			if encErr != nil {
				return nil, encErr
			}
			return Wrap(c.FormatID, errPayload)
		}

		respPayload, err := resp.Encode()
		if err != nil {
			return nil, &ErrEncode{Service: c.Service, Cause: err}
		}

		return Wrap(c.FormatID, respPayload)
	}
}

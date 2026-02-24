package horos

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// Test types: a concrete Req/Resp that implement Codec[T] via JSON.
// These simulate what a service would define.
// ---------------------------------------------------------------------------

type EchoRequest struct {
	Text string `json:"text"`
}

func (r EchoRequest) Encode() ([]byte, error) { return json.Marshal(r) }
func (r EchoRequest) Decode(data []byte) (EchoRequest, error) {
	var out EchoRequest
	err := json.Unmarshal(data, &out)
	return out, err
}

type EchoResponse struct {
	Echo string `json:"echo"`
}

func (r EchoResponse) Encode() ([]byte, error) { return json.Marshal(r) }
func (r EchoResponse) Decode(data []byte) (EchoResponse, error) {
	var out EchoResponse
	err := json.Unmarshal(data, &out)
	return out, err
}

// ---------------------------------------------------------------------------
// Envelope tests
// ---------------------------------------------------------------------------

func TestWrapUnwrap(t *testing.T) {
	payload := []byte(`{"text":"hello"}`)

	wrapped, err := Wrap(FormatJSON, payload)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}

	if len(wrapped) != HeaderSize+len(payload) {
		t.Fatalf("expected %d bytes, got %d", HeaderSize+len(payload), len(wrapped))
	}

	fmtID, out, err := Unwrap(wrapped)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if fmtID != FormatJSON {
		t.Fatalf("format ID: want %d, got %d", FormatJSON, fmtID)
	}
	if string(out) != string(payload) {
		t.Fatalf("payload: want %s, got %s", payload, out)
	}
}

func TestWrapRawFormat(t *testing.T) {
	payload := []byte(`raw data`)
	wrapped, err := Wrap(FormatRaw, payload)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	// Raw still gets an envelope header for unambiguous parsing.
	if len(wrapped) != HeaderSize+len(payload) {
		t.Fatalf("expected %d bytes, got %d", HeaderSize+len(payload), len(wrapped))
	}
	fmtID, out, err := Unwrap(wrapped)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if fmtID != FormatRaw {
		t.Fatalf("format ID: want %d, got %d", FormatRaw, fmtID)
	}
	if string(out) != string(payload) {
		t.Fatalf("payload: want %s, got %s", payload, out)
	}
}

func TestUnwrapTooShort(t *testing.T) {
	data := []byte("hi")
	fmtID, payload, err := Unwrap(data)
	if err != nil {
		t.Fatalf("Unwrap short data: %v", err)
	}
	if fmtID != FormatRaw {
		t.Fatalf("expected FormatRaw for short data, got %d", fmtID)
	}
	if string(payload) != "hi" {
		t.Fatalf("expected original data back, got %q", payload)
	}
}

func TestUnwrapChecksumMismatch(t *testing.T) {
	payload := []byte(`{"test":true}`)
	wrapped, _ := Wrap(FormatJSON, payload)

	// Corrupt one byte of the payload.
	wrapped[len(wrapped)-1] ^= 0xFF

	_, _, err := Unwrap(wrapped)
	if err == nil {
		t.Fatal("expected checksum error, got nil")
	}
	if !IsChecksumError(err) {
		t.Fatalf("expected ErrChecksum, got %T: %v", err, err)
	}
}

// ---------------------------------------------------------------------------
// ServiceError tests
// ---------------------------------------------------------------------------

func TestServiceErrorEncodeDecode(t *testing.T) {
	original := &ServiceError{
		Code:    "NOT_FOUND",
		Message: "node xyz not found",
		Service: "repvow",
	}

	data, err := original.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	decoded, err := ServiceError{}.Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if decoded.Code != original.Code {
		t.Fatalf("Code: want %s, got %s", original.Code, decoded.Code)
	}
	if decoded.Message != original.Message {
		t.Fatalf("Message: want %s, got %s", original.Message, decoded.Message)
	}
	if decoded.Service != original.Service {
		t.Fatalf("Service: want %s, got %s", original.Service, decoded.Service)
	}
}

func TestDetectError(t *testing.T) {
	svcErr := NewServiceError("BAD_REQUEST", "invalid input")
	data, _ := svcErr.Encode()

	detected, ok := DetectError(data)
	if !ok {
		t.Fatal("expected DetectError to find the error")
	}
	if detected.Code != "BAD_REQUEST" {
		t.Fatalf("code: want BAD_REQUEST, got %s", detected.Code)
	}
}

func TestDetectErrorNormal(t *testing.T) {
	data := []byte(`{"echo":"hello"}`)
	_, ok := DetectError(data)
	if ok {
		t.Fatal("normal payload should not be detected as error")
	}
}

func TestServiceErrorIs(t *testing.T) {
	err := NewServiceError("NOT_FOUND", "gone")
	if !errors.Is(err, ErrNotFound) {
		t.Fatal("expected errors.Is to match ErrNotFound")
	}
	if errors.Is(err, ErrBadRequest) {
		t.Fatal("should not match ErrBadRequest")
	}
}

func TestToServiceError(t *testing.T) {
	plain := errors.New("boom")
	se := ToServiceError(plain)
	if se.Code != "INTERNAL" {
		t.Fatalf("expected INTERNAL, got %s", se.Code)
	}

	original := NewServiceError("CONFLICT", "duplicate")
	se2 := ToServiceError(original)
	if se2.Code != "CONFLICT" {
		t.Fatalf("expected CONFLICT, got %s", se2.Code)
	}
}

func TestServiceErrorWithDetails(t *testing.T) {
	err := NewServiceError("BAD_REQUEST", "validation failed")
	detailed := err.WithDetails(map[string]string{"field": "name"})

	if detailed.Details == nil {
		t.Fatal("expected details to be set")
	}

	var m map[string]string
	if e := json.Unmarshal(detailed.Details, &m); e != nil {
		t.Fatalf("unmarshal details: %v", e)
	}
	if m["field"] != "name" {
		t.Fatalf("expected field=name, got %v", m)
	}

	if err.Details != nil {
		t.Fatal("original error should not have details")
	}
}

// ---------------------------------------------------------------------------
// Contract tests (round-trip: client call → handler → response)
// ---------------------------------------------------------------------------

func TestContractRoundTrip(t *testing.T) {
	contract := NewContract[EchoRequest, EchoResponse]("echo").WithFormat(FormatJSON)

	handler := contract.Handler(func(_ context.Context, req EchoRequest) (EchoResponse, error) {
		return EchoResponse{Echo: req.Text}, nil
	})

	caller := func(ctx context.Context, service string, payload []byte) ([]byte, error) {
		if service != "echo" {
			t.Fatalf("unexpected service: %s", service)
		}
		return handler(ctx, payload)
	}

	resp, err := contract.Call(context.Background(), caller, EchoRequest{Text: "ping"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Echo != "ping" {
		t.Fatalf("Echo: want ping, got %s", resp.Echo)
	}
}

func TestContractServiceError(t *testing.T) {
	contract := NewContract[EchoRequest, EchoResponse]("echo").WithFormat(FormatJSON)

	handler := contract.Handler(func(_ context.Context, _ EchoRequest) (EchoResponse, error) {
		return EchoResponse{}, NewServiceError("NOT_FOUND", "no such echo")
	})

	caller := func(ctx context.Context, _ string, payload []byte) ([]byte, error) {
		return handler(ctx, payload)
	}

	_, err := contract.Call(context.Background(), caller, EchoRequest{Text: "ping"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var svcErr *ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected *ServiceError, got %T: %v", err, err)
	}
	if svcErr.Code != "NOT_FOUND" {
		t.Fatalf("code: want NOT_FOUND, got %s", svcErr.Code)
	}
}

func TestContractRawFormat(t *testing.T) {
	contract := NewContract[EchoRequest, EchoResponse]("echo")

	handler := contract.Handler(func(_ context.Context, req EchoRequest) (EchoResponse, error) {
		return EchoResponse{Echo: req.Text + "!"}, nil
	})

	caller := func(ctx context.Context, _ string, payload []byte) ([]byte, error) {
		return handler(ctx, payload)
	}

	resp, err := contract.Call(context.Background(), caller, EchoRequest{Text: "raw"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Echo != "raw!" {
		t.Fatalf("Echo: want raw!, got %s", resp.Echo)
	}
}

// ---------------------------------------------------------------------------
// Registry tests (in-memory only — SQLite tests in registry_db_test.go)
// ---------------------------------------------------------------------------

func TestRegistryBuiltins(t *testing.T) {
	r := NewRegistry()

	info, ok := r.Lookup(FormatRaw)
	if !ok {
		t.Fatal("expected FormatRaw to be registered")
	}
	if info.Name != "raw" {
		t.Fatalf("expected raw, got %s", info.Name)
	}

	info, ok = r.Lookup(FormatJSON)
	if !ok {
		t.Fatal("expected FormatJSON to be registered")
	}
	if info.Name != "json" {
		t.Fatalf("expected json, got %s", info.Name)
	}
}

func TestRegistryRegister(t *testing.T) {
	r := NewRegistry()
	err := r.Register(FormatInfo{ID: 2, Name: "msgpack", MIME: "application/msgpack"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	info, ok := r.Lookup(2)
	if !ok {
		t.Fatal("expected format 2 to be registered")
	}
	if info.Name != "msgpack" {
		t.Fatalf("expected msgpack, got %s", info.Name)
	}
}

func TestRegistryConflict(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(FormatInfo{ID: 2, Name: "msgpack", MIME: "application/msgpack"})

	err := r.Register(FormatInfo{ID: 2, Name: "msgpack", MIME: "application/msgpack"})
	if err != nil {
		t.Fatalf("idempotent register should not fail: %v", err)
	}

	err = r.Register(FormatInfo{ID: 2, Name: "cbor", MIME: "application/cbor"})
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
}

func TestRegistryAll(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(FormatInfo{ID: 2, Name: "msgpack", MIME: "application/msgpack"})

	all := r.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 formats, got %d", len(all))
	}
}

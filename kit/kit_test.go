package kit

import (
	"context"
	"errors"
	"testing"
)

func TestChain_Order(t *testing.T) {
	var order []string

	mw := func(name string) Middleware {
		return func(next Endpoint) Endpoint {
			return func(ctx context.Context, req any) (any, error) {
				order = append(order, name+"_before")
				resp, err := next(ctx, req)
				order = append(order, name+"_after")
				return resp, err
			}
		}
	}

	base := func(_ context.Context, _ any) (any, error) {
		order = append(order, "endpoint")
		return "ok", nil
	}

	chained := Chain(mw("a"), mw("b"), mw("c"))(base)
	resp, err := chained(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp != "ok" {
		t.Fatalf("response: got %v", resp)
	}

	expected := []string{"a_before", "b_before", "c_before", "endpoint", "c_after", "b_after", "a_after"}
	if len(order) != len(expected) {
		t.Fatalf("order length: got %d, want %d", len(order), len(expected))
	}
	for i, v := range expected {
		if order[i] != v {
			t.Fatalf("order[%d]: got %q, want %q", i, order[i], v)
		}
	}
}

func TestChain_ErrorPropagation(t *testing.T) {
	errFail := errors.New("fail")
	base := func(_ context.Context, _ any) (any, error) {
		return nil, errFail
	}

	noop := func(next Endpoint) Endpoint { return next }
	chained := Chain(noop)(base)

	_, err := chained(context.Background(), nil)
	if !errors.Is(err, errFail) {
		t.Fatalf("error: got %v, want %v", err, errFail)
	}
}

func TestContext_UserID(t *testing.T) {
	ctx := context.Background()
	if v := GetUserID(ctx); v != "" {
		t.Fatalf("empty context: got %q", v)
	}

	ctx = WithUserID(ctx, "usr_123")
	if v := GetUserID(ctx); v != "usr_123" {
		t.Fatalf("after set: got %q", v)
	}
}

func TestContext_Handle(t *testing.T) {
	ctx := WithHandle(context.Background(), "alice")
	if v := GetHandle(ctx); v != "alice" {
		t.Fatalf("handle: got %q", v)
	}
}

func TestContext_Transport_Default(t *testing.T) {
	ctx := context.Background()
	if v := GetTransport(ctx); v != "http" {
		t.Fatalf("default transport: got %q, want 'http'", v)
	}
}

func TestContext_Transport_Set(t *testing.T) {
	ctx := WithTransport(context.Background(), "mcp_quic")
	if v := GetTransport(ctx); v != "mcp_quic" {
		t.Fatalf("transport: got %q", v)
	}
}

func TestContext_RequestID(t *testing.T) {
	ctx := WithRequestID(context.Background(), "req_abc")
	if v := GetRequestID(ctx); v != "req_abc" {
		t.Fatalf("request_id: got %q", v)
	}
}

func TestContext_TraceID(t *testing.T) {
	ctx := WithTraceID(context.Background(), "trc_xyz")
	if v := GetTraceID(ctx); v != "trc_xyz" {
		t.Fatalf("trace_id: got %q", v)
	}
}

func TestContext_EmptyDefaults(t *testing.T) {
	ctx := context.Background()
	if v := GetHandle(ctx); v != "" {
		t.Fatalf("handle default: got %q", v)
	}
	if v := GetRequestID(ctx); v != "" {
		t.Fatalf("request_id default: got %q", v)
	}
	if v := GetTraceID(ctx); v != "" {
		t.Fatalf("trace_id default: got %q", v)
	}
}

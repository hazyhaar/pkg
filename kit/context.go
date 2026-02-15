package kit

import "context"

type contextKey string

const (
	UserIDKey    contextKey = "kit_user_id"
	HandleKey    contextKey = "kit_handle"
	TransportKey contextKey = "kit_transport" // "http", "mcp_quic"
	RequestIDKey contextKey = "kit_request_id"
	TraceIDKey   contextKey = "kit_trace_id"
)

func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, UserIDKey, id)
}
func GetUserID(ctx context.Context) string {
	v, _ := ctx.Value(UserIDKey).(string)
	return v
}

func WithHandle(ctx context.Context, h string) context.Context {
	return context.WithValue(ctx, HandleKey, h)
}
func GetHandle(ctx context.Context) string {
	v, _ := ctx.Value(HandleKey).(string)
	return v
}

func WithTransport(ctx context.Context, t string) context.Context {
	return context.WithValue(ctx, TransportKey, t)
}
func GetTransport(ctx context.Context) string {
	if v, ok := ctx.Value(TransportKey).(string); ok {
		return v
	}
	return "http"
}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, RequestIDKey, id)
}
func GetRequestID(ctx context.Context) string {
	v, _ := ctx.Value(RequestIDKey).(string)
	return v
}

func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, TraceIDKey, id)
}
func GetTraceID(ctx context.Context) string {
	v, _ := ctx.Value(TraceIDKey).(string)
	return v
}

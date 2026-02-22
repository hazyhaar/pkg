package kit

import "context"

type contextKey string

const (
	UserIDKey    contextKey = "kit_user_id"
	HandleKey    contextKey = "kit_handle"
	TransportKey contextKey = "kit_transport" // "http", "mcp_quic"
	RequestIDKey contextKey = "kit_request_id"
	TraceIDKey   contextKey = "kit_trace_id"
	SessionIDKey contextKey = "kit_session_id"
	RemoteAddrKey contextKey = "kit_remote_addr"
	RoleKey      contextKey = "kit_role"
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

func WithSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, SessionIDKey, id)
}
func GetSessionID(ctx context.Context) string {
	v, _ := ctx.Value(SessionIDKey).(string)
	return v
}

func WithRemoteAddr(ctx context.Context, addr string) context.Context {
	return context.WithValue(ctx, RemoteAddrKey, addr)
}
func GetRemoteAddr(ctx context.Context) string {
	v, _ := ctx.Value(RemoteAddrKey).(string)
	return v
}

func WithRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, RoleKey, role)
}
func GetRole(ctx context.Context) string {
	v, _ := ctx.Value(RoleKey).(string)
	return v
}

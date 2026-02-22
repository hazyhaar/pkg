# kit — transport-agnostic endpoint primitives

`kit` defines the core `Endpoint` type and context helpers shared by all HOROS
services. A single endpoint serves both HTTP handlers and MCP tools through the
same middleware chain.

## Quick start

```go
type Endpoint func(ctx context.Context, request any) (response any, err error)
type Middleware func(Endpoint) Endpoint

endpoint := kit.Chain(
    audit.Middleware(logger, "action"),
    auth.RequireAuth,
)(baseEndpoint)
```

## Context helpers

```go
ctx = kit.WithUserID(ctx, "user123")
ctx = kit.WithRequestID(ctx, idgen.New())
ctx = kit.WithTransport(ctx, "mcp_quic")
ctx = kit.WithTraceID(ctx, traceID)

uid := kit.GetUserID(ctx)
```

## MCP bridge

`RegisterMCPTool` exposes a kit Endpoint as an MCP tool:

```go
kit.RegisterMCPTool(mcpServer, tool, endpoint, func(req *mcp.CallToolRequest) (*kit.MCPDecodeResult, error) {
    var input MyInput
    json.Unmarshal(req.Params.Arguments, &input)
    return &kit.MCPDecodeResult{Request: input}, nil
})
```

## Exported API

| Symbol | Description |
|--------|-------------|
| `Endpoint` | `func(ctx, request any) (response any, err error)` |
| `Middleware` | `func(Endpoint) Endpoint` |
| `Chain(outer, others...)` | Compose middlewares (outer is outermost) |
| `WithUserID` / `GetUserID` | User identity in context |
| `WithHandle` / `GetHandle` | Username in context |
| `WithTransport` / `GetTransport` | `"http"` or `"mcp_quic"` |
| `WithRequestID` / `GetRequestID` | Request correlation ID |
| `WithTraceID` / `GetTraceID` | Cross-service trace ID |
| `RegisterMCPTool` | Bridge Endpoint to MCP tool |
| `MCPDecodeResult` | Decoded request + context enrichment |

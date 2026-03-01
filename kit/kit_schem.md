╔══════════════════════════════════════════════════════════════════════════╗
║  kit — Transport-agnostic endpoint pattern + context keys + MCP bridge  ║
╠══════════════════════════════════════════════════════════════════════════╣
║                                                                        ║
║  ARCHITECTURE                                                          ║
║  ────────────                                                          ║
║                                                                        ║
║  Same business logic serves both HTTP and MCP transports:              ║
║                                                                        ║
║                  ┌────────────────┐                                    ║
║  HTTP request ──→│  HTTP Handler  │──┐                                 ║
║                  └────────────────┘  │                                 ║
║                                      ├──→ Endpoint(ctx, req) -> resp   ║
║                  ┌────────────────┐  │                                 ║
║  MCP tool call ─→│RegisterMCPTool│──┘                                 ║
║                  └────────────────┘                                    ║
║                          │                                             ║
║                          │  decode(*CallToolRequest)                   ║
║                          │  -> MCPDecodeResult{Request, EnrichCtx}     ║
║                          │                                             ║
║                          ▼                                             ║
║           ┌──────────────────────────┐                                 ║
║           │   Middleware Chain        │                                 ║
║           │   Chain(a, b, c)(ep)     │                                 ║
║           │   = a(b(c(ep)))          │                                 ║
║           └──────────────────────────┘                                 ║
║                                                                        ║
║  CONTEXT KEYS (typed, private — no collision risk)                     ║
║  ────────────────────────────────────────────────                      ║
║                                                                        ║
║  ┌──────────────────┬──────────────────┬──────────────────────────┐    ║
║  │ Key              │ Get/With funcs   │ Default                  │    ║
║  ├──────────────────┼──────────────────┼──────────────────────────┤    ║
║  │ UserIDKey        │ Get/WithUserID   │ ""                       │    ║
║  │ HandleKey        │ Get/WithHandle   │ ""                       │    ║
║  │ TransportKey     │ Get/WithTransport│ "http" (if missing)      │    ║
║  │ RequestIDKey     │ Get/WithRequestID│ ""                       │    ║
║  │ TraceIDKey       │ Get/WithTraceID  │ ""                       │    ║
║  │ SessionIDKey     │ Get/WithSessionID│ ""                       │    ║
║  │ RemoteAddrKey    │ Get/WithRemoteAddr│""                       │    ║
║  │ RoleKey          │ Get/WithRole     │ ""                       │    ║
║  └──────────────────┴──────────────────┴──────────────────────────┘    ║
║                                                                        ║
║  All keys use private type `contextKey string` — not raw strings.      ║
║  GetTransport() returns "http" when no value set (safe default).       ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  EXPORTED TYPES                                                        ║
║  ──────────────                                                        ║
║                                                                        ║
║  Endpoint    func(ctx context.Context, request any) (any, error)       ║
║  Middleware  func(Endpoint) Endpoint                                   ║
║  MCPDecodeResult {                                                     ║
║      Request   any                                                     ║
║      EnrichCtx func(context.Context) context.Context                   ║
║  }                                                                     ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  KEY FUNCTIONS                                                         ║
║  ─────────────                                                         ║
║                                                                        ║
║  Chain(outer Middleware, others ...Middleware) Middleware                ║
║      Compose middlewares: first arg = outermost wrapper.               ║
║      Chain(a,b,c)(ep) == a(b(c(ep)))                                  ║
║                                                                        ║
║  RegisterMCPTool(                                                      ║
║      srv *mcp.Server,                                                  ║
║      tool *mcp.Tool,                                                   ║
║      endpoint Endpoint,                                                ║
║      decode func(*mcp.CallToolRequest) (*MCPDecodeResult, error),      ║
║  )                                                                     ║
║      Registers an Endpoint as an MCP tool on the SDK server.           ║
║      decode: extracts typed request from MCP arguments.                ║
║      EnrichCtx: propagates identity from MCP into context.             ║
║      Errors: result.SetError() for tool errors (not return error).     ║
║      Response: JSON-marshaled into TextContent.                        ║
║                                                                        ║
║  MCP ERROR HANDLING:                                                   ║
║  ┌─────────────────────────────────────────────────────────────┐       ║
║  │ decode error  -> result.SetError(err), return (result, nil) │       ║
║  │ endpoint error -> result.SetError(err), return (result, nil)│       ║
║  │ marshal error  -> result.SetError(err), return (result, nil)│       ║
║  │ NEVER return (nil, error) — that's a JSON-RPC protocol err  │       ║
║  └─────────────────────────────────────────────────────────────┘       ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  FILES                                                                 ║
║  ─────                                                                 ║
║                                                                        ║
║  context.go       Context keys + With/Get pairs (8 keys)              ║
║  endpoint.go      Endpoint, Middleware, Chain()                        ║
║  transport_mcp.go RegisterMCPTool, MCPDecodeResult                    ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES                                                          ║
║  ────────────                                                          ║
║                                                                        ║
║  External: github.com/modelcontextprotocol/go-sdk/mcp                 ║
║  Stdlib  : context, encoding/json, errors, fmt                        ║
║  No pkg/ internal dependencies. Leaf package.                          ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  DEPENDANTS (within pkg/)                                              ║
║  ────────────────────────                                              ║
║                                                                        ║
║  mcpquic/server (WithTransport, WithSessionID, WithRemoteAddr)         ║
║  mcprt/policy, shield/trace, trace/driver, auth/middleware,            ║
║  audit/middleware                                                      ║
║  External: repvow, horum, horostracker, chrc (all services)           ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  DATABASE TABLES                                                       ║
║  ───────────────                                                       ║
║  None. Pure logic, no storage.                                         ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  INVARIANTS                                                            ║
║  ──────────                                                            ║
║                                                                        ║
║  - GetTransport() defaults to "http" if context has no value           ║
║  - RegisterMCPTool NEVER returns error — uses SetError() for all       ║
║    failures (decode, endpoint, marshal)                                ║
║  - MCP arguments arrive as json.RawMessage, NOT map[string]any         ║
║  - EnrichCtx in MCPDecodeResult is the mechanism to propagate          ║
║    identity from MCP into the shared endpoint context                  ║
║  - Context keys are private type — no collision with other packages    ║
║                                                                        ║
╚══════════════════════════════════════════════════════════════════════════╝

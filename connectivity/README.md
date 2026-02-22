# connectivity — smart service routing with hot-reload

`connectivity` dispatches service calls to local (in-memory) or remote
(HTTP, MCP-over-QUIC) handlers based on a SQLite routes table. Changing a row
switches a service from monolith to microservice with zero downtime.

```
router.Call(ctx, "billing", payload)
        │
        ▼
  ┌─────────────┐    routes table
  │   Router     │◄── watch (PRAGMA data_version)
  └──────┬──────┘
         │ strategy?
    ┌────┼────┬────────┐
    ▼    ▼    ▼        ▼
  local  http  mcp    noop
  (mem) (POST) (QUIC)  (∅)
```

## Quick start

```go
router := connectivity.New(
    connectivity.WithLogger(logger),
)
router.RegisterLocal("billing", billingHandler)
router.RegisterTransport("http", connectivity.HTTPFactory())
router.RegisterTransport("mcp", connectivity.MCPFactory())

connectivity.Init(db)
go connectivity.Watch(ctx, db, 2*time.Second)

result, err := router.Call(ctx, "billing", payload)
```

## Routes table

```sql
CREATE TABLE routes (
    service_name TEXT PRIMARY KEY,
    strategy     TEXT NOT NULL,  -- local, http, mcp, quic, dbsync, noop
    endpoint     TEXT,
    config       TEXT,           -- JSON, transport-specific
    updated_at   INTEGER
);
```

| Strategy | Dispatch |
|----------|----------|
| `local` | In-memory function call |
| `http` | HTTP POST to endpoint (SSRF-validated) |
| `mcp` | MCP tool call over QUIC |
| `noop` | Silently returns nil |

## Middleware

Handler middlewares compose via `Chain`:

```go
handler = connectivity.Chain(
    connectivity.Logging(logger),
    connectivity.Timeout(5*time.Second),
    connectivity.WithRetry(3, 500*time.Millisecond, logger),
    connectivity.WithCircuitBreaker(cb, "billing"),
    connectivity.WithFallback(localHandler, "billing", logger),
)(handler)
```

## Circuit breaker

```go
cb := connectivity.NewCircuitBreaker(
    connectivity.WithBreakerThreshold(5),      // 5 failures → open
    connectivity.WithBreakerResetTimeout(30*time.Second),
    connectivity.WithBreakerHalfOpenMax(2),     // 2 successes → closed
)
```

States: Closed → Open (on threshold) → HalfOpen (after timeout) → Closed (on success).

## Exported API

| Symbol | Description |
|--------|-------------|
| `Router` | Service dispatcher with local/remote routing |
| `Handler` | `func(ctx, payload []byte) ([]byte, error)` |
| `TransportFactory` | Creates handlers from endpoint + config |
| `HTTPFactory()` | HTTP POST transport with SSRF guard |
| `MCPFactory()` | MCP-over-QUIC transport |
| `CircuitBreaker` | Three-state circuit breaker |
| `Chain(mws...)` | Compose handler middlewares |
| `WithRetry`, `WithFallback`, `WithCircuitBreaker` | Resilience middlewares |

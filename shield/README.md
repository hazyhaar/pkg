# shield — HTTP security middleware

`shield` provides composable HTTP middlewares for security headers, rate
limiting, request tracing, body limits, and flash messages.

## Quick start

```go
// Create tables (BO only — FO receives them via dbsync).
shield.Init(db)

// Pre-built stacks.
foStack, mm := shield.DefaultFOStack(db)  // maintenance + headers + body limit + trace + rate limit + flash
mm.StartReloader(done)                    // poll maintenance flag every 5s
boStack := shield.DefaultBOStack()    // headers + body limit + trace + flash (no rate limit)

mux.Handle("/", foStack(handler))
```

## Middlewares

| Middleware | Description |
|------------|-------------|
| `NewMaintenanceMode(db, excludes)` | 503 page when maintenance flag is active in SQLite |
| `SecurityHeaders(cfg)` | CSP, X-Frame-Options, X-Content-Type-Options, Referrer-Policy, Permissions-Policy |
| `MaxFormBody(maxBytes)` | Limit `application/x-www-form-urlencoded` body size |
| `TraceID` | Generate random trace ID, set `X-Trace-ID` header, enrich context logger |
| `NewRateLimiter(db, excludes)` | Per-IP rate limiting with rules from SQLite |
| `Flash` | Read one-time flash cookie, inject into context |
| `HeadToGet` | Forward HEAD requests as GET |

## Rate limiting

Rules are stored in a `rate_limits` SQLite table and reloaded every 60 s.
Buckets are tracked in memory per IP + endpoint.

```go
rl := shield.NewRateLimiter(db, []string{"/static/"})
rl.StartReloader(done)
mux.Handle("/api/", rl.Middleware()(handler))
```

API endpoints get a `429` JSON response with `Retry-After` header. Page
endpoints get a flash-message redirect.

## Flash messages

```go
// Set a flash (cookie-based, one-time read).
shield.SetFlash(w, "success", "Saved!")

// Read in next request (via middleware).
flash := shield.GetFlash(r.Context())
// flash.Type = "success", flash.Message = "Saved!"
```

## Exported API

| Symbol | Description |
|--------|-------------|
| `SecurityHeaders(cfg)` | Security header middleware |
| `DefaultHeaders()` | Sensible default header config |
| `MaxFormBody(max)` | Body size limit |
| `TraceID` | Request tracing middleware |
| `NewRateLimiter(db, excludes)` | SQLite-backed rate limiter |
| `Flash` | Flash message middleware |
| `SetFlash(w, type, msg)` | Write flash cookie |
| `GetFlash(ctx)` | Read flash from context |
| `HeadToGet` | HEAD → GET conversion |
| `NewMaintenanceMode(db, excludes)` | SQLite-backed maintenance mode |
| `Schema` | DDL for rate_limits + maintenance tables |
| `Init(db)` | Create shield tables (idempotent) |
| `DefaultFOStack(db)` | FO middleware stack (returns stack + MaintenanceMode) |
| `DefaultBOStack()` | BO middleware stack |

```
╔════════════════════════════════════════════════════════════════════════════╗
║  shield — Reusable HTTP security middleware stack for HOROS services      ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  MIDDLEWARE CHAIN (DefaultFOStack order)                                  ║
║  ───────────────────────────────────────                                  ║
║                                                                          ║
║  HTTP Request                                                            ║
║       │                                                                  ║
║       v                                                                  ║
║  ┌────────────────────┐  active=1 in maintenance table                   ║
║  │ 1. MaintenanceMode │──> 503 + HTML page (bypasses: /healthz, /static) ║
║  └────────┬───────────┘                                                  ║
║           v                                                              ║
║  ┌────────────────────┐                                                  ║
║  │ 2. HeadToGet       │──> HEAD -> GET (net/http strips body)            ║
║  └────────┬───────────┘                                                  ║
║           v                                                              ║
║  ┌────────────────────┐  CSP, X-Frame-Options, nosniff,                  ║
║  │ 3. SecurityHeaders │  Referrer-Policy, Permissions-Policy             ║
║  └────────┬───────────┘                                                  ║
║           v                                                              ║
║  ┌────────────────────┐                                                  ║
║  │ 4. MaxFormBody     │──> 64 KiB limit on form-urlencoded POST          ║
║  └────────┬───────────┘  (other content types pass through)              ║
║           v                                                              ║
║  ┌────────────────────┐  4-byte random -> 8 hex chars                    ║
║  │ 5. TraceID         │──> ctx: kit.TraceIDKey + LoggerKey               ║
║  │                    │──> header: X-Trace-ID                            ║
║  │                    │──> slog: trace_id, method, path, remote_addr     ║
║  └────────┬───────────┘                                                  ║
║           v                                                              ║
║  ┌────────────────────┐  per-IP, per-endpoint token bucket               ║
║  │ 6. RateLimiter     │  rules from rate_limits table (reload 60s)       ║
║  │                    │  /api/*: 429 JSON                                ║
║  │                    │  other:  303 redirect + flash cookie             ║
║  │                    │  GC expired buckets every 5 min                   ║
║  └────────┬───────────┘                                                  ║
║           v                                                              ║
║  ┌────────────────────┐  "flash" cookie -> ctx FlashKey                  ║
║  │ 7. Flash           │  parses "success:" / "error:" prefix             ║
║  │                    │  clears cookie after read                         ║
║  └────────┬───────────┘                                                  ║
║           v                                                              ║
║     Application Handler                                                  ║
║                                                                          ║
║  DefaultBOStack (no RateLimiter, no MaintenanceMode):                    ║
║    HeadToGet -> SecurityHeaders -> MaxFormBody -> TraceID -> Flash        ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  DATABASE TABLES (SQLite, created by shield.Init(db))                    ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  rate_limits                                                             ║
║  ┌────────────────┬─────────┬─────────────────────────────────────┐      ║
║  │ endpoint       │ TEXT    │ PK, e.g. "GET /api/search"          │      ║
║  │ max_requests   │ INTEGER │ default 60, requests per window     │      ║
║  │ window_seconds │ INTEGER │ default 60, sliding window size     │      ║
║  │ enabled        │ INTEGER │ default 1, 0=disabled               │      ║
║  └────────────────┴─────────┴─────────────────────────────────────┘      ║
║                                                                          ║
║  maintenance                                                             ║
║  ┌────────────────┬─────────┬─────────────────────────────────────┐      ║
║  │ id             │ INTEGER │ PK, CHECK (id = 1) — single row    │      ║
║  │ active         │ INTEGER │ default 0, 1=maintenance on         │      ║
║  │ message        │ TEXT    │ user-facing message                 │      ║
║  └────────────────┴─────────┴─────────────────────────────────────┘      ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  KEY TYPES                                                               ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  RateLimiter      per-IP/endpoint rate limiting, SQLite-backed rules     ║
║    .db            *sql.DB (reads rate_limits table)                      ║
║    .rules         map[string]RateLimitConfig (cached, reloaded 60s)      ║
║    .buckets       sync.Map (key: "ip:endpoint" -> *bucket{count,reset})  ║
║    .exclude       []string (path prefixes to skip)                       ║
║                                                                          ║
║  RateLimitConfig  {MaxRequests int, WindowSeconds int, Enabled bool}     ║
║                                                                          ║
║  MaintenanceMode  503 gate, SQLite-backed flag (single-row table)        ║
║    .active        atomic.Bool (cached, reloaded every 5s)                ║
║    .message       atomic.Value (string)                                  ║
║    .exclude       []string (bypass prefixes: /healthz, /static/)         ║
║    .page          []byte (custom HTML or default)                        ║
║                                                                          ║
║  HeaderConfig     {CSP, XFrameOptions, XContentTypeOptions,              ║
║                    ReferrerPolicy, PermissionsPolicy}                    ║
║                                                                          ║
║  FlashMessage     {Type: "success"|"error", Message: string}             ║
║                                                                          ║
║  contextKey       "shield_logger" (LoggerKey), "shield_flash" (FlashKey) ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  KEY FUNCTIONS                                                           ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  DefaultFOStack(db) ([]Middleware, *MaintenanceMode)                     ║
║    Returns ordered FO middleware stack + MaintenanceMode handle           ║
║                                                                          ║
║  DefaultBOStack() []Middleware                                           ║
║    Returns ordered BO middleware stack (no rate limit, no maintenance)    ║
║                                                                          ║
║  Init(db) error                                                          ║
║    Creates rate_limits + maintenance tables (idempotent)                  ║
║                                                                          ║
║  SecurityHeaders(HeaderConfig) Middleware                                ║
║  DefaultHeaders() HeaderConfig                                           ║
║  MaxFormBody(maxBytes) Middleware                                         ║
║  TraceID(next) http.Handler                                              ║
║  HeadToGet(next) http.Handler                                            ║
║  Flash(next) http.Handler                                                ║
║  SetFlash(w, flashType, message)                                         ║
║  GetFlash(ctx) *FlashMessage                                             ║
║  GetLogger(ctx) *slog.Logger                                             ║
║  ExtractIP(r) string  (X-Forwarded-For first IP or RemoteAddr)           ║
║                                                                          ║
║  NewRateLimiter(db, ...excludePrefixes) *RateLimiter                     ║
║  RateLimiter.Middleware(next) http.Handler                               ║
║  RateLimiter.StartReloader(done <-chan struct{})                          ║
║  RateLimiter.SetDB(db)  (for dbsync DB swap on FO)                       ║
║                                                                          ║
║  NewMaintenanceMode(db, ...excludePrefixes) *MaintenanceMode             ║
║  MaintenanceMode.Middleware(next) http.Handler                           ║
║  MaintenanceMode.StartReloader(done <-chan struct{})                      ║
║  MaintenanceMode.SetDB(db)  (for dbsync DB swap on FO)                   ║
║  MaintenanceMode.SetPage(html []byte)                                    ║
║  MaintenanceMode.Active() bool                                           ║
║  MaintenanceMode.Message() string                                        ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES (hazyhaar/pkg/*)                                           ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  kit    WithTraceID, GetTraceID (context key for trace correlation)       ║
║                                                                          ║
║  No other internal dependencies. Pure middleware package.                 ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  FLASH COOKIE PROTOCOL                                                   ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  SetFlash(w, "error", "msg")                                             ║
║    -> Set-Cookie: flash=error%3Amsg; Path=/; MaxAge=10; HttpOnly; Lax    ║
║                                                                          ║
║  Flash middleware reads on next request:                                  ║
║    -> parse "error:msg" or "success:msg"                                 ║
║    -> store FlashMessage in context under FlashKey                        ║
║    -> clear cookie (MaxAge=-1)                                           ║
║                                                                          ║
║  GetFlash(ctx) -> *FlashMessage{Type:"error", Message:"msg"}            ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  SECURITY HEADERS (DefaultHeaders)                                       ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  Content-Security-Policy:                                                ║
║    default-src 'self'; script-src 'self'; style-src 'self'               ║
║    'unsafe-inline'; img-src 'self' data: https:; frame-ancestors 'none'  ║
║  X-Frame-Options: DENY                                                   ║
║  X-Content-Type-Options: nosniff                                         ║
║  Referrer-Policy: strict-origin-when-cross-origin                        ║
║  Permissions-Policy: camera=(), microphone=(), geolocation=()            ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  RATE LIMIT BEHAVIOR                                                     ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  /api/* paths -> HTTP 429 JSON {"error":"rate limit exceeded"}           ║
║                  + Retry-After: 60                                       ║
║  other paths  -> HTTP 303 redirect to Referer + flash cookie "error:..."║
║                  + Retry-After: 60                                       ║
║                                                                          ║
║  Bucket key: "ip:METHOD path" (e.g. "1.2.3.4:POST /api/upload")         ║
║  Window: sliding, resets on expiry                                       ║
║  GC: expired buckets purged every 5 minutes                              ║
║                                                                          ║
╚════════════════════════════════════════════════════════════════════════════╝
```

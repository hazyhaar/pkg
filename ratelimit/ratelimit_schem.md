╔═══════════════════════════════════════════════════════════════════════════════╗
║  ratelimit — Generic sliding-window rate limiter: SQLite rules + memory fast ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  ARCHITECTURE                                                                ║
║  ~~~~~~~~~~~~                                                                ║
║                                                                              ║
║  ┌─────────────────────┐     Reload()      ┌─────────────────────┐           ║
║  │ rate_limiter_rules  │ ────────────────> │    Limiter          │           ║
║  │ (SQLite)            │     every 60s     │                     │           ║
║  │                     │ <──── AddRule ──── │  rules map[string]  │           ║
║  │                     │ <── RemoveRule ─── │  RuleConfig         │           ║
║  └─────────────────────┘                   │                     │           ║
║                                            │  buckets sync.Map   │           ║
║                                            │  (in-memory fast    │           ║
║  ┌──────────────────┐                      │   path, per-key)    │           ║
║  │ HTTP Request     │                      └─────────┬───────────┘           ║
║  │ (any client)     │                                │                       ║
║  └────────┬─────────┘                                │                       ║
║           │                                          │                       ║
║           v                                          v                       ║
║  ┌──────────────────┐    Allow(key)    ┌──────────────────────────┐           ║
║  │ HTTPMiddleware   │ ──────────────> │ Sliding Window Check     │           ║
║  │ key="ip:{IP}"    │                 │                          │           ║
║  │ X-Forwarded-For  │                 │ 1. Check DB rule for key │           ║
║  │ or RemoteAddr    │                 │    (overrides defaults)  │           ║
║  └──────────────────┘                 │ 2. LoadOrStore bucket    │           ║
║                                       │ 3. If window expired:    │           ║
║  ┌──────────────────┐                 │    reset counter         │           ║
║  │ MCP Tool Call    │                 │ 4. Increment count       │           ║
║  │ (any tool)       │                 │ 5. count > limit?        │           ║
║  └────────┬─────────┘                 │    -> ErrRateLimited     │           ║
║           │                           │    -> nil (allowed)      │           ║
║           v                           └──────────────────────────┘           ║
║  ┌──────────────────┐                                                        ║
║  │ MCPMiddleware    │ ── Allow("tool:{name}") ──>                            ║
║  │ -> PolicyFunc    │                                                        ║
║  │ for mcprt        │                                                        ║
║  └──────────────────┘                                                        ║
║                                                                              ║
║  BACKGROUND GOROUTINES (StartReloader)                                       ║
║  ┌────────────┐ 60s    ┌────────────┐ 5min   ┌───────────┐                  ║
║  │ reload     │ ─────> │ gc         │ ─────> │ cleanup   │                  ║
║  │ rules from │ ticker │ expired    │ ticker │ stale     │                  ║
║  │ DB         │        │ buckets    │        │ buckets   │                  ║
║  └────────────┘        └────────────┘        └───────────┘                  ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  DATABASE TABLE                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  rate_limiter_rules                                                          ║
║  ├── rule_key TEXT PK              (e.g. "ip:1.2.3.4", "tool:my_tool")     ║
║  ├── max_requests INTEGER DEFAULT 60                                         ║
║  ├── window_seconds INTEGER DEFAULT 60                                       ║
║  ├── is_active INTEGER DEFAULT 1   (0=disabled, 1=enabled)                  ║
║  ├── created_at INTEGER (epoch)                                              ║
║  └── updated_at INTEGER                                                      ║
║                                                                              ║
║  Rule evaluation: DB rule overrides programmatic defaults.                   ║
║  If no DB rule exists for a key, the limit/window passed to Allow() is used.║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED TYPES                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  Limiter            struct  Core rate limiter: DB rules + sync.Map buckets   ║
║  RuleConfig         struct  MaxRequests int, WindowSeconds int, IsActive     ║
║  RuleEntry          struct  Key, MaxRequests, WindowSeconds, IsActive        ║
║  Option             func    Functional option for New()                       ║
║  MCPMiddlewareFunc  func    func(ctx, toolName) error (mcprt PolicyFunc)     ║
║                                                                              ║
║  Variables:                                                                  ║
║  ErrRateLimited     error   Sentinel error for limit exceeded                ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  KEY FUNCTIONS                                                               ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  New(db, ...Option) *Limiter                                                 ║
║  WithClock(fn) Option                  -- injectable clock for testing        ║
║                                                                              ║
║  Limiter.Init() error                  -- create rate_limiter_rules table     ║
║  Limiter.Reload() error                -- load active rules from DB          ║
║  Limiter.StartReloader(ctx)            -- bg: reload 60s + GC 5min           ║
║                                                                              ║
║  Limiter.Allow(ctx, key, limit, window) error                                ║
║    -- core check: returns nil or ErrRateLimited                              ║
║    -- DB rule for key overrides limit/window args                            ║
║                                                                              ║
║  Limiter.AllowN(ctx, key, n, limit, window) error                            ║
║    -- consume n tokens (calls Allow n times)                                 ║
║                                                                              ║
║  Limiter.AddRule(key, maxReq, windowSec) error                               ║
║    -- upsert into rate_limiter_rules (call Reload after)                     ║
║                                                                              ║
║  Limiter.RemoveRule(key) error                                               ║
║    -- set is_active=0                                                        ║
║                                                                              ║
║  Limiter.ListRules() ([]RuleEntry, error)                                    ║
║    -- all rules, active and inactive                                         ║
║                                                                              ║
║  Limiter.HTTPMiddleware(limit, window) func(http.Handler) http.Handler       ║
║    -- rate limit by client IP                                                ║
║    -- key format: "ip:{client_ip}"                                           ║
║    -- IP from X-Forwarded-For (first) or RemoteAddr                         ║
║    -- 429 response: JSON for /api/* paths, plain text otherwise             ║
║    -- sets Retry-After header                                                ║
║                                                                              ║
║  Limiter.MCPMiddleware(limit, window) MCPMiddlewareFunc                      ║
║    -- rate limit by tool name                                                ║
║    -- key format: "tool:{tool_name}"                                         ║
║    -- compatible with mcprt.PolicyFunc signature                             ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES                                                                ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  Standard library only: database/sql, net/http, sync, time, log/slog        ║
║  No internal hazyhaar/pkg dependencies.                                      ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  ALGORITHM DETAIL                                                            ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  Sliding window counter (per-key):                                           ║
║    bucket { count int; resetAt time.Time }                                   ║
║    stored in sync.Map keyed by string                                        ║
║                                                                              ║
║    Allow(key):                                                               ║
║      1. Lock-free LoadOrStore -> if new, count=1, resetAt=now+window, OK    ║
║      2. If now > resetAt -> reset: count=1, resetAt=now+window, OK          ║
║      3. count++ -> if count > limit -> ErrRateLimited                       ║
║      4. Otherwise -> OK                                                      ║
║                                                                              ║
║    GC (every 5min): range over sync.Map, delete expired buckets             ║
║                                                                              ║
║  Key naming convention:                                                      ║
║    "ip:{addr}"    -- HTTP middleware                                         ║
║    "tool:{name}"  -- MCP middleware                                          ║
║    "{custom}"     -- direct Allow() calls                                    ║
║                                                                              ║
╚═══════════════════════════════════════════════════════════════════════════════╝

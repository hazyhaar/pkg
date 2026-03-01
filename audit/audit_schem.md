╔══════════════════════════════════════════════════════════════════════════════╗
║  audit — Async SQLite audit logger with batch writes + kit middleware      ║
╠══════════════════════════════════════════════════════════════════════════════╣
║                                                                            ║
║  FLOW (Async path — preferred)                                             ║
║  ─────────────────────────────                                             ║
║                                                                            ║
║  ┌───────────┐  LogAsync(entry)  ┌────────────────────────┐                ║
║  │ Caller /  │ ───────────────→ │ SQLiteLogger           │                ║
║  │ Middleware│                   │                        │                ║
║  │           │  fillDefaults()   │  ┌──────────────────┐  │                ║
║  │           │  - gen EntryID    │  │ ch (chan, buf=256)│  │                ║
║  │           │  - set Timestamp  │  │                  │  │                ║
║  │           │  - infer Status   │  └────────┬─────────┘  │                ║
║  └───────────┘                   │           │            │                ║
║                                  │           ▼            │                ║
║                                  │  ┌──────────────────┐  │  INSERT INTO   ║
║                                  │  │ flushLoop()      │  │  audit_log     ║
║                                  │  │ goroutine        │ ─┼──────────→ DB  ║
║                                  │  │                  │  │                ║
║                                  │  │ flush when:      │  │                ║
║                                  │  │  - batch >= 32   │  │                ║
║                                  │  │  - ticker 500ms  │  │                ║
║                                  │  │  - ch closed     │  │                ║
║                                  │  └──────────────────┘  │                ║
║                                  └────────────────────────┘                ║
║                                                                            ║
║  FLOW (Sync path)                                                          ║
║  ────────────────                                                          ║
║  caller ──→ Log(ctx, entry) ──→ fillDefaults() ──→ insert() ──→ DB        ║
║                                                                            ║
║  FLOW (Middleware — wraps kit.Endpoint)                                     ║
║  ──────────────────────────────────────                                     ║
║  ┌──────────┐         ┌────────────────────┐        ┌────────────┐         ║
║  │ request  │ ──────→ │ Middleware(logger,  │ ────→ │ next(ctx,  │         ║
║  │ (any)    │         │   actionName)      │        │   request) │         ║
║  └──────────┘         │                    │        └──────┬─────┘         ║
║                       │ captures:          │               │               ║
║                       │  - duration_ms     │  ◄────────────┘               ║
║                       │  - params (JSON)   │                               ║
║                       │  - result (JSON)   │  LogAsync(entry)              ║
║                       │  - error           │ ───────────────→ logger       ║
║                       │  - transport (kit) │                               ║
║                       │  - user_id (kit)   │                               ║
║                       │  - request_id (kit)│                               ║
║                       └────────────────────┘                               ║
║                                                                            ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  DATABASE TABLE: audit_log                                                 ║
║  ─────────────────────────                                                 ║
║  entry_id      TEXT PK        -- UUID via idgen (prefix "aud_")            ║
║  timestamp     INTEGER        -- Unix epoch seconds                        ║
║  action        TEXT NOT NULL   -- action name (e.g. "user.login")          ║
║  transport     TEXT DEFAULT 'http'  -- "http" or "mcp_quic"                ║
║  user_id       TEXT           -- who performed the action                  ║
║  request_id    TEXT           -- correlation ID                            ║
║  parameters    TEXT           -- JSON of request params                    ║
║  result        TEXT           -- JSON of response (on success)             ║
║  error_message TEXT           -- error string (on failure)                 ║
║  duration_ms   INTEGER        -- wall-clock duration                       ║
║  status        TEXT DEFAULT 'success'  -- "success" or "error"             ║
║                                                                            ║
║  Indexes: timestamp, action, user_id                                       ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED TYPES                                                            ║
║  ──────────────                                                            ║
║  Entry     struct { EntryID, Action, Transport, UserID, RequestID,         ║
║                     Parameters, Result, Error, Status string;              ║
║                     Timestamp, DurationMs int64 }                          ║
║                                                                            ║
║  Logger    interface {                                                     ║
║                Log(ctx, *Entry) error    -- sync write                     ║
║                LogAsync(*Entry)          -- async, non-blocking            ║
║                Close() error             -- drain + wait                   ║
║            }                                                               ║
║                                                                            ║
║  SQLiteLogger  struct (implements Logger)                                  ║
║  Option        func(*SQLiteLogger)                                         ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED FUNCTIONS                                                        ║
║  ──────────────────                                                        ║
║  NewSQLiteLogger(db *sql.DB, opts ...Option) *SQLiteLogger                 ║
║  WithIDGenerator(gen idgen.Generator) Option                               ║
║  (l *SQLiteLogger) Init() error          -- CREATE TABLE IF NOT EXISTS     ║
║  (l *SQLiteLogger) Log(ctx, *Entry) error                                  ║
║  (l *SQLiteLogger) LogAsync(*Entry)      -- drops if buffer full (warn)    ║
║  (l *SQLiteLogger) Close() error         -- drain ch, wait for flushLoop   ║
║  Middleware(logger Logger, actionName string) kit.Middleware                ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES (internal)                                                   ║
║  ────────────────────────                                                  ║
║  github.com/hazyhaar/pkg/idgen  -- ID generation (Prefixed "aud_")         ║
║  github.com/hazyhaar/pkg/kit    -- context helpers (UserID, RequestID,     ║
║                                    Transport, Handle), Endpoint type,      ║
║                                    Middleware type                          ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  INVARIANTS                                                                ║
║  ──────────                                                                ║
║  - Async buffer = 256 entries; overflow is silently dropped (slog.Warn)    ║
║  - flushLoop: flush at batch>=32 OR every 500ms OR on channel close        ║
║  - Close() drains channel then blocks on done chan (no data loss on exit)   ║
║  - Init() MUST be called before any writes (creates audit_log table)       ║
║  - LogAsync after Close() = panic (channel closed)                         ║
║  - fillDefaults: auto-generates EntryID, Timestamp; infers Status          ║
║    from Error field ("error" if Error!="", else "success")                 ║
║  - Transport defaults to "http" if empty                                   ║
╚══════════════════════════════════════════════════════════════════════════════╝

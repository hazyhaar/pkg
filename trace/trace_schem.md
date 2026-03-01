```
╔════════════════════════════════════════════════════════════════════════════╗
║  trace — Transparent SQL tracing via "sqlite-trace" driver wrapper       ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  ARCHITECTURE                                                            ║
║  ────────────                                                            ║
║                                                                          ║
║  Application code:                                                       ║
║    import _ "github.com/hazyhaar/pkg/trace"  // registers driver         ║
║    db, _ := sql.Open("sqlite-trace", "app.db")                           ║
║                                                                          ║
║  ┌───────────────────────────────────────────────────────────────┐       ║
║  │  database/sql layer                                           │       ║
║  │                                                               │       ║
║  │  db.Exec("INSERT ...")  /  db.Query("SELECT ...")             │       ║
║  │       │                                                       │       ║
║  │       v                                                       │       ║
║  │  ┌──────────────────────────────────────────────┐             │       ║
║  │  │  "sqlite-trace" driver (TracingDriver)       │             │       ║
║  │  │                                              │             │       ║
║  │  │  TracingDriver.Open(name)                    │             │       ║
║  │  │    └──> tracingConn                          │             │       ║
║  │  │           .Prepare(query)                    │             │       ║
║  │  │           .PrepareContext(ctx, query)         │             │       ║
║  │  │             └──> tracingStmt                 │             │       ║
║  │  │                    .ExecContext(ctx, args)    │             │       ║
║  │  │                    .QueryContext(ctx, args)   │             │       ║
║  │  │                    .Exec(args)               │             │       ║
║  │  │                    .Query(args)              │             │       ║
║  │  │                       │                      │             │       ║
║  │  │                       v                      │             │       ║
║  │  │              ┌─────────────────┐             │             │       ║
║  │  │              │ record(ctx,     │             │             │       ║
║  │  │              │   op, duration, │             │             │       ║
║  │  │              │   err)          │             │             │       ║
║  │  │              └────┬────────────┘             │             │       ║
║  │  │                   │                          │             │       ║
║  │  │        ┌──────────┴──────────┐               │             │       ║
║  │  │        │                     │               │             │       ║
║  │  │        v                     v               │             │       ║
║  │  │  ┌──────────┐    ┌────────────────┐          │             │       ║
║  │  │  │ slog     │    │ globalStore    │          │             │       ║
║  │  │  │ adaptive │    │ .RecordAsync() │          │             │       ║
║  │  │  │ levels   │    │ (non-blocking) │          │             │       ║
║  │  │  └──────────┘    └───────┬────────┘          │             │       ║
║  │  │                          │                   │             │       ║
║  │  │                 ┌────────┴────────┐          │             │       ║
║  │  │                 │                 │          │             │       ║
║  │  │                 v                 v          │             │       ║
║  │  │          ┌────────────┐   ┌──────────────┐  │             │       ║
║  │  │          │ Store      │   │ RemoteStore  │  │             │       ║
║  │  │          │ (local     │   │ (HTTP POST   │  │             │       ║
║  │  │          │  SQLite)   │   │  to BO)      │  │             │       ║
║  │  │          └────────────┘   └──────────────┘  │             │       ║
║  │  └──────────────────────────────────────────────┘             │       ║
║  │                                                               │       ║
║  │       Wraps: modernc.org/sqlite driver                        │       ║
║  └───────────────────────────────────────────────────────────────┘       ║
║                                                                          ║
║  SLOG ADAPTIVE LEVELS                                                    ║
║  ────────────────────                                                    ║
║                                                                          ║
║  err != nil        -> slog.Error                                         ║
║  duration > 100ms  -> slog.Warn                                          ║
║  normal            -> slog.Debug                                         ║
║                                                                          ║
║  Attributes: component="sql", op, query, duration, trace_id, error       ║
║                                                                          ║
║  PRAGMA FILTERING                                                        ║
║  ────────────────                                                        ║
║                                                                          ║
║  Queries starting with "PRAGMA " are SKIPPED unless:                     ║
║    - duration >= 10ms (slow PRAGMA = investigate)                        ║
║    - err != nil (failed PRAGMA = always log)                             ║
║  This eliminates 99.5% of dbsync watcher noise (PRAGMA data_version     ║
║  every 200ms).                                                           ║
║                                                                          ║
║  PERSISTENCE: LOCAL STORE (BO or standalone)                             ║
║  ───────────────────────────────────────────                              ║
║                                                                          ║
║  traceDB, _ := sql.Open("sqlite", "traces.db")  // raw driver!          ║
║  store := trace.NewStore(traceDB)                                        ║
║  store.Init()  // CREATE TABLE sql_traces                                ║
║  trace.SetStore(store)                                                   ║
║                                                                          ║
║  Async pattern:                                                          ║
║    RecordAsync(entry) -> ch (cap 1024) -> flushLoop -> flushBatch        ║
║    Batch size: 64 entries                                                ║
║    Flush interval: 1 second                                              ║
║    Buffer full: drop silently (no backpressure on app)                   ║
║    Close(): drain channel, flush remaining, stop goroutine               ║
║                                                                          ║
║  PERSISTENCE: REMOTE STORE (FO -> BO)                                   ║
║  ────────────────────────────────────                                     ║
║                                                                          ║
║  rs := trace.NewRemoteStore("https://bo/api/internal/traces", nil)       ║
║  trace.SetStore(rs)                                                      ║
║                                                                          ║
║  Same async pattern as Store:                                            ║
║    ch (cap 1024) -> flushLoop -> json.Marshal(batch) -> HTTP POST        ║
║    Client timeout: 5s                                                    ║
║    On error: slog.Error (no retry, drop batch)                           ║
║                                                                          ║
║  INGEST HANDLER (BO side, receives from RemoteStore)                     ║
║  ──────────────────────────────────────────────────                       ║
║                                                                          ║
║  mux.Handle("/api/internal/traces", trace.IngestHandler(store))          ║
║    POST application/json []*Entry (max 1 MB)                             ║
║    -> store.RecordAsync for each entry                                   ║
║    -> 204 No Content on success                                          ║
║    -> 405 (wrong method), 400 (bad JSON)                                 ║
║                                                                          ║
║  ┌──────────┐   HTTP POST []*Entry   ┌─────────────┐   RecordAsync      ║
║  │ FO       │ ─────────────────────> │ BO          │ ──────────────>    ║
║  │ Remote   │   /api/internal/traces │ IngestHandler│   Store.flushLoop  ║
║  │ Store    │                        │             │   -> sql_traces    ║
║  └──────────┘                        └─────────────┘                    ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  DATABASE TABLE (SQLite, raw "sqlite" driver — NOT "sqlite-trace")       ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  sql_traces                                                              ║
║  ┌─────────────┬─────────┬────────────────────────────────────────┐      ║
║  │ id          │ INTEGER │ PK AUTOINCREMENT                       │      ║
║  │ trace_id    │ TEXT    │ HTTP/MCP request correlation            │      ║
║  │ op          │ TEXT    │ NOT NULL, "Exec" or "Query"             │      ║
║  │ query       │ TEXT    │ NOT NULL, SQL statement                 │      ║
║  │ duration_us │ INTEGER │ NOT NULL, microseconds                  │      ║
║  │ error       │ TEXT    │ empty if success                        │      ║
║  │ timestamp   │ INTEGER │ NOT NULL, unix microseconds             │      ║
║  └─────────────┴─────────┴────────────────────────────────────────┘      ║
║                                                                          ║
║  INDEXES:                                                                ║
║    idx_sql_traces_ts    ON sql_traces(timestamp)                         ║
║    idx_sql_traces_tid   ON sql_traces(trace_id) WHERE trace_id != ''     ║
║    idx_sql_traces_slow  ON sql_traces(duration_us) WHERE duration_us > 100000 ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  KEY TYPES                                                               ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  Entry          {TraceID, Op, Query, DurationUs, Error, Timestamp}       ║
║  Recorder       interface { RecordAsync(*Entry); Close() error }         ║
║  Store          local SQLite persistence (implements Recorder)           ║
║  RemoteStore    HTTP POST to BO endpoint (implements Recorder)           ║
║  TracingDriver  driver.Driver wrapper (registered as "sqlite-trace")     ║
║  tracingConn    driver.Conn wrapper (intercepts Prepare)                 ║
║  tracingStmt    driver.Stmt wrapper (intercepts Exec/Query, records)     ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  KEY FUNCTIONS                                                           ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  SetStore(Recorder)       Set global trace recorder (Store or Remote)    ║
║  NewStore(db) *Store      Create local SQLite trace store                ║
║  Store.Init() error       CREATE TABLE sql_traces (idempotent)           ║
║  Store.RecordAsync(*Entry)  Queue entry for async batch insert           ║
║  Store.Close() error      Drain + stop flush goroutine                   ║
║  NewRemoteStore(url, client) *RemoteStore    HTTP POST recorder          ║
║  RemoteStore.RecordAsync(*Entry)  Queue for async HTTP batch push        ║
║  RemoteStore.Close() error        Drain + stop flush goroutine           ║
║  IngestHandler(store) http.HandlerFunc  BO-side batch receiver           ║
║                                                                          ║
║  init()  Registers "sqlite-trace" driver wrapping modernc.org/sqlite     ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES (hazyhaar/pkg/*)                                           ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  kit              GetTraceID(ctx) for request correlation                 ║
║  modernc.org/sqlite  Underlying SQLite driver being wrapped              ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  CRITICAL INVARIANTS                                                     ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  1. Store DB MUST use raw "sqlite" driver, NEVER "sqlite-trace"          ║
║     (infinite recursion: traced writes trigger more traced writes)        ║
║                                                                          ║
║  2. RecordAsync is non-blocking: full buffer = silent drop               ║
║     (never add backpressure to application queries)                      ║
║                                                                          ║
║  3. "sqlite-trace" is registered in init() via sql.Register              ║
║     (import side-effect is sufficient)                                    ║
║                                                                          ║
║  4. Store and RemoteStore use identical async pattern:                    ║
║     chan(1024) -> batch(64) -> flush(1s tick)                             ║
║                                                                          ║
╚════════════════════════════════════════════════════════════════════════════╝
```

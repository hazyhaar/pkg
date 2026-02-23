# trace — transparent SQL tracing for SQLite

`trace` wraps the `modernc.org/sqlite` driver at the `database/sql/driver` level.
Switching from `"sqlite"` to `"sqlite-trace"` records every Exec and Query
without changing application code.

```
sql.Open("sqlite-trace", "app.db")
        │
        ▼
  TracingDriver.Open
        │
        ▼
  tracingConn.Prepare ─► tracingStmt
        │                    │
        ▼                    ▼
  driver.Conn          Exec / Query
                             │
                    ┌────────┴────────┐
                    ▼                 ▼
              slog (adaptive)   Store (async SQLite)
              Debug < 100ms     batch 64 / flush 1s
              Warn  ≥ 100ms    channel 1024 (drop-on-full)
              Error on failure
```

## Quick start

```go
import _ "github.com/hazyhaar/pkg/trace"

// 1. Set up the trace store (uses raw "sqlite" to avoid recursion).
traceDB, _ := sql.Open("sqlite", "traces.db")
store := trace.NewStore(traceDB)
store.Init()
trace.SetStore(store)
defer store.Close()

// 2. Open your app database with the tracing driver.
db, _ := sql.Open("sqlite-trace", "app.db")
// All queries are now traced automatically.
```

## How it works

1. **Driver wrapping** — `TracingDriver` wraps every `Conn` and `Stmt` returned by
   the base SQLite driver. The `init()` function registers it as `"sqlite-trace"`.

2. **Adaptive logging** — Each query is logged via `slog` at a level that depends
   on duration: `Debug` under 100 ms, `Warn` at 100 ms+, `Error` on failure.
   The trace ID from `kit.GetTraceID(ctx)` is included when present.

3. **Async persistence** — If a `Store` is configured, entries are sent to a
   1024-capacity channel. A background goroutine batches up to 64 entries or
   flushes every second, inserting them in a single transaction.

## Schema

```sql
CREATE TABLE sql_traces (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    trace_id   TEXT,
    op         TEXT NOT NULL,       -- "Exec" or "Query"
    query      TEXT NOT NULL,
    duration_us INTEGER NOT NULL,
    error      TEXT,
    timestamp  INTEGER NOT NULL     -- unix microseconds
);
```

Indexes on `timestamp`, `trace_id` (partial, non-empty), and `duration_us`
(partial, > 100 000 us) for slow-query analysis.

## Remote tracing (FO → BO)

In a FO/BO split architecture, the FO has no local SQLite for traces.
`RemoteStore` batches entries and POSTs them over HTTPS to the BO, which
ingests them into its local `Store`. This follows the same HTTPS pattern as
`authproxy` and `dbsync.WriteProxy`.

```
  FO                                    BO
  ┌──────────────┐   HTTPS POST   ┌──────────────┐
  │ RemoteStore   │──────────────▶│ IngestHandler │
  │ (batch 64,    │  JSON []*Entry │ (RecordAsync  │
  │  flush 1s)    │               │  into Store)  │
  └──────────────┘               └──────────────┘
```

**FO side:**

```go
rs := trace.NewRemoteStore("https://bo.internal/api/internal/traces", nil)
trace.SetStore(rs)
defer rs.Close()
```

**BO side:**

```go
mux.Handle("/api/internal/traces", trace.IngestHandler(store))
```

## Exported API

| Symbol | Description |
|--------|-------------|
| `Recorder` | Interface for trace backends (`RecordAsync` + `Close`) |
| `Store` | Async trace writer with batching (local SQLite) |
| `NewStore(db)` | Create store (db must use raw `"sqlite"` driver) |
| `RemoteStore` | Async trace forwarder (HTTPS POST to BO) |
| `NewRemoteStore(url, client)` | Create remote store (nil client = 5s default) |
| `IngestHandler(store)` | HTTP handler for BO trace ingestion endpoint |
| `Entry` | Single trace record |
| `SetStore(s)` | Set / replace global recorder (nil disables persistence) |
| `Schema` | DDL string for manual migration |

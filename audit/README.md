# audit — async SQLite audit logging

`audit` records endpoint calls to an `audit_log` table with async buffering.
A kit middleware captures duration, parameters, result, and error automatically.

## Quick start

```go
logger := audit.NewSQLiteLogger(db,
    audit.WithIDGenerator(idgen.Prefixed("aud_", idgen.Default)),
)
logger.Init()
defer logger.Close()

// Wrap any kit.Endpoint — captures timing, params, result, user, transport.
endpoint = kit.Chain(
    audit.Middleware(logger, "create_dossier"),
)(endpoint)
```

## How it works

1. The middleware calls the next endpoint, measures wall-clock time, and builds
   an `Entry` from the context (`kit.GetUserID`, `kit.GetTransport`, etc.).
2. The entry is sent to `LogAsync`, which pushes it to a 256-capacity channel.
3. A background goroutine collects entries in batches of 32 or flushes every
   500 ms, inserting them into SQLite.
4. If the channel is full, the entry is dropped with a warning log.

## Schema

```sql
CREATE TABLE audit_log (
    entry_id      TEXT PRIMARY KEY,
    timestamp     INTEGER NOT NULL,
    action        TEXT NOT NULL,
    transport     TEXT NOT NULL DEFAULT 'http',
    user_id       TEXT,
    request_id    TEXT,
    parameters    TEXT,
    result        TEXT,
    error_message TEXT,
    duration_ms   INTEGER,
    status        TEXT NOT NULL DEFAULT 'success'
);
```

Indexes on `timestamp`, `action`, `user_id`.

## Exported API

| Symbol | Description |
|--------|-------------|
| `SQLiteLogger` | Async audit writer (256-entry buffer, 32-batch, 500 ms flush) |
| `NewSQLiteLogger(db, opts)` | Create logger and start flush goroutine |
| `Entry` | Audit trail record |
| `Logger` | Interface: `Log`, `LogAsync`, `Close` |
| `Middleware(logger, action)` | Kit middleware capturing endpoint metadata |
| `WithIDGenerator(gen)` | Option to override ID generation |

# watch — reactive change detection for SQLite

`watch` provides a generic poll → detect → debounce → action loop. All
hot-reload systems in the ecosystem (connectivity, channels, mcprt, dbsync) use
it as their change-detection backbone.

```
┌─────────┐   tick    ┌──────────┐  changed?  ┌──────────┐  debounce  ┌────────┐
│  Ticker  │────────►│ Detector  │──────────►│ Debounce  │──────────►│ Action  │
│ (1s def) │         │ (PRAGMA)  │           │ (opt.)    │           │ reload  │
└─────────┘         └──────────┘           └──────────┘           └────────┘
```

## Quick start

```go
w := watch.New(db, watch.Options{
    Interval: 200 * time.Millisecond,
    Debounce: 500 * time.Millisecond,
})
go w.OnChange(ctx, func() error {
    return service.Reload(ctx, db)
})
```

## Built-in detectors

| Detector | Source | Use case |
|----------|--------|----------|
| `PragmaDataVersion()` | `PRAGMA data_version` | Cross-connection writes (default) |
| `PragmaUserVersion()` | `PRAGMA user_version` | Application-controlled versioning |
| `MaxColumnDetector(table, col)` | `MAX(column)` | Timestamp / auto-increment columns |

`PRAGMA data_version` increments whenever another connection writes to the
database, making it ideal for detecting external changes without triggers.

## WaitForVersion

`WaitForVersion` blocks until the observed version reaches a target. Useful for
tests and synchronization between producer and consumer.

```go
// Writer bumps user_version after inserting config.
db.Exec("PRAGMA user_version = 42")

// Reader waits for version 42 before proceeding.
w.WaitForVersion(ctx, 42)
```

## Observability

```go
stats := w.Stats()
// stats.Checks          — total polls
// stats.ChangesDetected — version changes seen
// stats.Reloads         — successful action calls
// stats.AvgReloadTime   — mean action duration
// stats.Errors          — detector or action errors
```

## Exported API

| Symbol | Description |
|--------|-------------|
| `Watcher` | Poll loop with debounce and stats |
| `New(db, opts)` | Create watcher (does not start polling) |
| `Options` | Interval, Debounce, Detector, Logger |
| `ChangeDetector` | `func(ctx, db) (int64, error)` |
| `PragmaDataVersion` | Default detector |
| `PragmaUserVersion` | App-controlled detector |
| `MaxColumnDetector` | Factory for `MAX(col)` detectors |
| `Stats` | Point-in-time counters |

╔══════════════════════════════════════════════════════════════════════════╗
║  dbopen -- Open SQLite with HOROS production-safe pragmas + BUSY retry  ║
╠══════════════════════════════════════════════════════════════════════════╣
║  Module: github.com/hazyhaar/pkg/dbopen                                ║
║  Files:  dbopen.go, retry.go                                           ║
║  Deps:   stdlib only (database/sql, fmt, log/slog, os, path/filepath,  ║
║          testing, context, strings, time) -- leaf package, no int deps  ║
╚══════════════════════════════════════════════════════════════════════════╝

OPEN FLOW
==========

  path string ──> Open(path, opts...)
                      │
                      ├── mkdirAll? -> os.MkdirAll(dir, 0o755)
                      │
                      ├── buildDSN(path, cfg) ──────────────────────────────┐
                      │                                                     │
                      │   DSN = path                                        │
                      │       + ?_txlock=immediate                          │
                      │       + &_pragma=busy_timeout(10000)                │
                      │       + &_pragma=journal_mode(WAL)                  │
                      │       + &_pragma=foreign_keys(1)                    │
                      │       + &_pragma=synchronous(NORMAL)                │
                      │       + [&_pragma=cache_size(N)]                    │
                      │                                                     │
                      ├── sql.Open(driver, dsn) <───────────────────────────┘
                      │
                      ├── exec schemaFiles (in order)
                      ├── exec inline schemas (in order)
                      │
                      ├── db.Ping() (unless WithoutPing)
                      │
                      └── return *sql.DB, error

WHY DSN _pragma= INSTEAD OF db.Exec("PRAGMA ...")
===================================================

  database/sql pools connections. A post-Open db.Exec("PRAGMA ...") only
  hits ONE connection. Other connections in the pool get default values
  (busy_timeout=0 -> instant SQLITE_BUSY).

  _pragma= in the DSN is applied by the modernc driver PER-CONNECTION.
  Every pooled connection gets the same pragmas. This is critical.

  ┌─────────────────────────────────────────────┐
  │  Connection Pool                            │
  │  ┌───────┐ ┌───────┐ ┌───────┐             │
  │  │ conn1 │ │ conn2 │ │ conn3 │  ... each   │
  │  │ WAL   │ │ WAL   │ │ WAL   │  gets ALL   │
  │  │ FK=ON │ │ FK=ON │ │ FK=ON │  pragmas     │
  │  │ 10s   │ │ 10s   │ │ 10s   │  via DSN    │
  │  └───────┘ └───────┘ └───────┘              │
  └─────────────────────────────────────────────┘

DEFAULT PRAGMAS
================

  ┌────────────────┬──────────────┬─────────────────────────────────┐
  │ Pragma         │ Default      │ Purpose                         │
  ├────────────────┼──────────────┼─────────────────────────────────┤
  │ journal_mode   │ WAL          │ concurrent reads during writes  │
  │ busy_timeout   │ 10000 (10s)  │ wait for locks, not instant err │
  │ foreign_keys   │ ON (1)       │ enforce FK constraints          │
  │ synchronous    │ NORMAL       │ balance durability / performance│
  │ _txlock        │ immediate    │ BEGIN IMMEDIATE, not DEFERRED   │
  └────────────────┴──────────────┴─────────────────────────────────┘

  Optional: cache_size (negative = KiB, e.g. -64000 = 64 MB)

OPTIONS (functional pattern)
=============================

  WithDriver(name)         -- SQL driver name (default "sqlite")
  WithTrace()              -- shorthand for WithDriver("sqlite-trace")
  WithBusyTimeout(ms)      -- PRAGMA busy_timeout (default 10000)
  WithCacheSize(pages)     -- PRAGMA cache_size
  WithSynchronous(mode)    -- PRAGMA synchronous (default "NORMAL")
  WithMkdirAll()           -- create parent dirs before open
  WithSchema(sql)          -- inline SQL after pragmas
  WithSchemaFile(path)     -- .sql file after pragmas
  WithoutPing()            -- skip db.Ping() verification
  WithoutForeignKeys()     -- disable foreign_keys (rarely needed)

TESTING HELPER
===============

  OpenMemory(t testing.TB, opts...) *sql.DB
      │
      ├── Open(":memory:", opts...)
      ├── db.SetMaxOpenConns(1)  <-- CRITICAL: each ":memory:" conn = separate DB
      ├── t.Cleanup(db.Close)
      └── return *sql.DB

RETRY (retry.go)
==================

  ┌──────────┐     ┌────────────────┐     ┌──────────┐
  │  Caller  │ ──> │ RunTx / Exec   │ ──> │ SQLite   │
  └──────────┘     │                │     └────┬─────┘
                   │ retry 3x       │          │
                   │ backoff:       │  BUSY?   │
                   │  100ms/200ms/  │ <────────┘
                   │  300ms         │
                   └────────────────┘

  RunTx(ctx, db, func(tx) error) error
      Wraps fn in BeginTx/Commit with auto-retry on SQLITE_BUSY.
      Max 3 attempts. Backoff: 100ms, 200ms, 300ms.
      Rollbacks on fn error. Respects context cancellation.

  Exec(ctx, db, query, args...) (sql.Result, error)
      Same retry logic for standalone statements.

  IsBusy(err) bool
      Checks for "SQLITE_BUSY", "database is locked",
      "database table is locked" in error message.

  sleepCtx(ctx, d) error
      Context-aware sleep. Returns ctx.Err() if cancelled during wait.

CHECKPOINT LOOP (dbopen.go)
============================

  CheckpointLoop(ctx, db, interval, logger)
      │
      ├── time.NewTicker(interval)
      │
      └── loop:
           ├── <-ctx.Done() → return
           └── <-ticker.C
                │
                └── PRAGMA wal_checkpoint(PASSIVE)
                     ├── OK  → logger.Info("wal checkpoint", log, checkpointed)
                     └── err → logger.Warn("wal checkpoint failed", error)

  Prevents unbounded WAL growth when multiple services share a SQLite
  database. PASSIVE mode does not block writers. Typically called as a
  background goroutine:

    go dbopen.CheckpointLoop(ctx, db, 5*time.Minute, logger)

  Consumers: HORAG (cmd/horag/main.go)

EXPORTED FUNCTIONS SUMMARY
===========================

  Open(path string, opts ...Option) (*sql.DB, error)
  OpenMemory(t testing.TB, opts ...Option) *sql.DB
  CheckpointLoop(ctx context.Context, db *sql.DB, interval time.Duration, logger *slog.Logger)
  RunTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error
  Exec(ctx context.Context, db *sql.DB, query string, args ...any) (sql.Result, error)
  IsBusy(err error) bool

DRIVER PREREQUISITE
====================

  Caller MUST blank-import the driver before calling Open:

    import _ "modernc.org/sqlite"             // default "sqlite"
    import _ "github.com/hazyhaar/pkg/trace"  // "sqlite-trace"

  Never use mattn/go-sqlite3 (CGO). Always modernc.org/sqlite (pure Go).

NO HTTP, NO MIDDLEWARE, NO DATABASE TABLES (just opens databases)

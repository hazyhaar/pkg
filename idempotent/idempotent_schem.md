╔══════════════════════════════════════════════════════════════════════════╗
║  idempotent — SQLite-backed idempotency guard (execute-once semantics)  ║
╠══════════════════════════════════════════════════════════════════════════╣
║                                                                        ║
║  FLOW                                                                  ║
║  ────                                                                  ║
║                                                                        ║
║  caller key ──→ SHA-256 ──→ ┌──────────────────────┐                   ║
║  + fn()                     │  Guard.Once(ctx,key)  │                   ║
║                             └──────────┬───────────┘                   ║
║                                        │                               ║
║                          ┌─────────────▼──────────────┐                ║
║                          │ SELECT by key_hash          │               ║
║                          └─────────────┬──────────────┘                ║
║                               ┌────────┴────────┐                     ║
║                               │                 │                     ║
║                          found?=YES         found?=NO                 ║
║                               │                 │                     ║
║                    ┌──────────▼───┐    ┌────────▼─────────┐           ║
║                    │ Return cached │    │ Execute fn()     │           ║
║                    │ result/error  │    │ INSERT result    │           ║
║                    └──────────────┘    │ (ON CONFLICT     │           ║
║                                        │  DO NOTHING)     │           ║
║                                        └────────┬─────────┘           ║
║                                                 │                     ║
║                                        ┌────────▼─────────┐           ║
║                                        │ Return result     │           ║
║                                        │ (or fn error)     │           ║
║                                        └──────────────────┘           ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  DATABASE TABLE                                                        ║
║  ──────────────                                                        ║
║                                                                        ║
║  idempotent_log                                                        ║
║  ┌──────────────┬──────────┬──────────────────────────────────────┐    ║
║  │ Column       │ Type     │ Notes                                │    ║
║  ├──────────────┼──────────┼──────────────────────────────────────┤    ║
║  │ key_hash     │ TEXT PK  │ SHA-256 hex of caller key            │    ║
║  │ original_key │ TEXT     │ NOT NULL, plaintext for debugging    │    ║
║  │ result       │ BLOB     │ Cached fn() return value             │    ║
║  │ error_msg    │ TEXT     │ fn() error string (if failed)        │    ║
║  │ created_at   │ INTEGER  │ Unix epoch, DEFAULT strftime now     │    ║
║  └──────────────┴──────────┴──────────────────────────────────────┘    ║
║  INDEX: idx_idempotent_created ON (created_at)                         ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  EXPORTED TYPES                                                        ║
║  ──────────────                                                        ║
║                                                                        ║
║  Guard struct { db *sql.DB }                                           ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  KEY FUNCTIONS                                                         ║
║  ─────────────                                                         ║
║                                                                        ║
║  New(db *sql.DB) *Guard                                                ║
║      Create a Guard backed by given database.                          ║
║                                                                        ║
║  (*Guard).Init() error                                                 ║
║      CREATE TABLE IF NOT EXISTS (idempotent).                          ║
║                                                                        ║
║  (*Guard).Once(ctx, key string, fn func()([]byte, error))              ║
║           -> ([]byte, error)                                           ║
║      Core method. Execute fn ONCE per key. Cache result.               ║
║      If fn errors, error is stored and returned on retries             ║
║      (fn is NOT re-executed on error).                                 ║
║                                                                        ║
║  (*Guard).Seen(ctx, key string) -> (bool, error)                       ║
║      Check if key was already processed.                               ║
║                                                                        ║
║  (*Guard).Prune(ctx, olderThan time.Duration) -> (int64, error)        ║
║      Delete entries older than duration. Returns deleted count.        ║
║                                                                        ║
║  (*Guard).Delete(ctx, key string) -> error                             ║
║      Remove specific key, allowing re-execution.                       ║
║                                                                        ║
║  (*Guard).Count(ctx) -> (int64, error)                                 ║
║      Number of entries in the log.                                     ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES                                                          ║
║  ────────────                                                          ║
║                                                                        ║
║  stdlib only: context, crypto/sha256, database/sql, encoding/hex, fmt, ║
║               time                                                     ║
║  No pkg/ internal dependencies.                                        ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  DATA FORMAT                                                           ║
║  ───────────                                                           ║
║                                                                        ║
║  Input : arbitrary string key + func() -> ([]byte, error)              ║
║  Output: []byte (opaque, caller-defined)                               ║
║  Storage: key hashed via SHA-256 hex. Result stored as BLOB.           ║
║  Errors stored as TEXT (fn error message string).                       ║
║                                                                        ║
╠══════════════════════════════════════════════════════════════════════════╣
║  BEHAVIOR NOTES                                                        ║
║  ──────────────                                                        ║
║                                                                        ║
║  - Failed fn() -> error stored, never retried. Use Delete() to reset.  ║
║  - INSERT uses ON CONFLICT DO NOTHING for race safety.                 ║
║  - If INSERT fails but fn succeeded, returns result anyway             ║
║    (loses idempotency guarantee, does not fail the operation).         ║
║  - Schema const exported for manual migration if needed.               ║
║                                                                        ║
╚══════════════════════════════════════════════════════════════════════════╝

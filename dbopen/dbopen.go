// CLAUDE:SUMMARY Opens SQLite databases with HOROS production-safe pragmas via DSN _pragma= (per-connection), schema loading, and test helpers.
// CLAUDE:DEPENDS
// CLAUDE:EXPORTS Open, OpenMemory, CheckpointLoop, Option, WithDriver, WithTrace, WithBusyTimeout, WithCacheSize, WithSynchronous, WithMkdirAll, WithSchema, WithSchemaFile, WithoutPing, WithoutForeignKeys

// Package dbopen provides a single function to open an SQLite database with
// the HOROS production-safe pragmas applied via EXEC (driver-agnostic).
//
// Default pragmas:
//
//	foreign_keys = ON
//	journal_mode = WAL
//	busy_timeout = 10000
//	synchronous  = NORMAL
//
// Usage:
//
//	import _ "modernc.org/sqlite"
//	db, err := dbopen.Open("app.db")
//
// With tracing driver:
//
//	import _ "github.com/hazyhaar/pkg/trace"
//	db, err := dbopen.Open("app.db", dbopen.WithTrace())
//
// In tests:
//
//	db := dbopen.OpenMemory(t)
package dbopen

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type config struct {
	driver      string
	busyTimeout int
	cacheSize   int
	synchronous string
	foreignKeys bool
	mkdirAll    bool
	schemas     []string
	schemaFiles []string
	ping        bool
}

func defaults() config {
	return config{
		driver:      "sqlite",
		busyTimeout: 10_000,
		synchronous: "NORMAL",
		foreignKeys: true,
		ping:        true,
	}
}

// Option customises Open behaviour.
type Option func(*config)

// WithDriver sets the database/sql driver name. Default: "sqlite".
func WithDriver(name string) Option { return func(c *config) { c.driver = name } }

// WithTrace is shorthand for WithDriver("sqlite-trace").
func WithTrace() Option { return WithDriver("sqlite-trace") }

// WithBusyTimeout sets PRAGMA busy_timeout in milliseconds. Default: 10000.
func WithBusyTimeout(ms int) Option { return func(c *config) { c.busyTimeout = ms } }

// WithCacheSize sets PRAGMA cache_size. 0 (default) keeps the SQLite default.
// Negative values are KiB (e.g. -64000 = 64 MB).
func WithCacheSize(pages int) Option { return func(c *config) { c.cacheSize = pages } }

// WithSynchronous sets PRAGMA synchronous. Default: "NORMAL".
func WithSynchronous(mode string) Option { return func(c *config) { c.synchronous = mode } }

// WithMkdirAll creates parent directories of the database path before opening.
func WithMkdirAll() Option { return func(c *config) { c.mkdirAll = true } }

// WithSchema queues inline SQL to execute after pragmas are applied.
func WithSchema(s string) Option { return func(c *config) { c.schemas = append(c.schemas, s) } }

// WithSchemaFile queues an .sql file to read and execute after pragmas.
func WithSchemaFile(path string) Option {
	return func(c *config) { c.schemaFiles = append(c.schemaFiles, path) }
}

// WithoutPing skips the db.Ping() verification after opening.
func WithoutPing() Option { return func(c *config) { c.ping = false } }

// WithoutForeignKeys disables PRAGMA foreign_keys (rarely needed).
func WithoutForeignKeys() Option { return func(c *config) { c.foreignKeys = false } }

// Open opens an SQLite database at path with HOROS production-safe pragmas.
// The caller must blank-import the appropriate driver before calling Open:
//
//	import _ "modernc.org/sqlite"           // default "sqlite" driver
//	import _ "github.com/hazyhaar/pkg/trace" // "sqlite-trace" driver
//
// CLAUDE:WARN Open requires a blank-imported SQLite driver before call (import _ "modernc.org/sqlite"). Panic at runtime otherwise.
func Open(path string, opts ...Option) (*sql.DB, error) {
	cfg := defaults()
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.mkdirAll && path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("dbopen: mkdir: %w", err)
		}
	}

	// Build DSN with _txlock=immediate and _pragma= for all production pragmas.
	// _txlock=immediate: all transactions use BEGIN IMMEDIATE (no DEFERRED promotion failures).
	// _pragma=: applied per-connection by the driver — critical because database/sql pools
	// connections, and a post-Open db.Exec("PRAGMA ...") only hits one connection.
	dsn := buildDSN(path, &cfg)

	db, err := sql.Open(cfg.driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("dbopen: open: %w", err)
	}

	for _, f := range cfg.schemaFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("dbopen: read schema file %s: %w", f, err)
		}
		if _, err := db.Exec(string(data)); err != nil {
			db.Close()
			return nil, fmt.Errorf("dbopen: exec schema file %s: %w", f, err)
		}
	}

	for _, s := range cfg.schemas {
		if _, err := db.Exec(s); err != nil {
			db.Close()
			return nil, fmt.Errorf("dbopen: exec schema: %w", err)
		}
	}

	if cfg.ping {
		if err := db.Ping(); err != nil {
			db.Close()
			return nil, fmt.Errorf("dbopen: ping: %w", err)
		}
	}

	return db, nil
}

// OpenMemory opens an in-memory SQLite database for testing.
// It sets MaxOpenConns(1) to ensure all queries hit the same in-memory
// database (each connection to ":memory:" creates a separate database).
// It registers t.Cleanup to close the database automatically.
//
// CLAUDE:WARN OpenMemory forces MaxOpenConns(1) — each :memory: connection is a separate DB. Do not override MaxOpenConns.
func OpenMemory(t testing.TB, opts ...Option) *sql.DB {
	t.Helper()
	db, err := Open(":memory:", opts...)
	if err != nil {
		t.Fatalf("dbopen.OpenMemory: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

// buildDSN constructs a modernc.org/sqlite DSN with _txlock=immediate and
// _pragma= parameters. The _pragma= parameters are applied per-connection
// by the driver, which is critical for database/sql connection pools.
func buildDSN(path string, cfg *config) string {
	fk := "1"
	if !cfg.foreignKeys {
		fk = "0"
	}

	dsn := path + "?_txlock=immediate"
	dsn += fmt.Sprintf("&_pragma=busy_timeout(%d)", cfg.busyTimeout)
	dsn += "&_pragma=journal_mode(WAL)"
	dsn += "&_pragma=foreign_keys(" + fk + ")"
	dsn += "&_pragma=synchronous(" + cfg.synchronous + ")"

	if cfg.cacheSize != 0 {
		dsn += fmt.Sprintf("&_pragma=cache_size(%d)", cfg.cacheSize)
	}

	return dsn
}

// CheckpointLoop runs PRAGMA wal_checkpoint(PASSIVE) on db at the given
// interval until ctx is cancelled. This is necessary when multiple long-lived
// processes share a WAL-mode database: SQLite's auto-checkpoint cannot fire
// while any process holds an active read transaction, causing the WAL to grow
// unbounded. PASSIVE is non-blocking — it checkpoints only pages not currently
// needed by readers.
//
// Call in a goroutine: go dbopen.CheckpointLoop(ctx, db, 1*time.Hour, logger)
//
// CLAUDE:WARN CheckpointLoop runs in a goroutine (blocking loop). Must be called as `go CheckpointLoop(...)`. Cancellation via ctx.
func CheckpointLoop(ctx context.Context, db *sql.DB, interval time.Duration, logger *slog.Logger, hooks ...func(string, string, ...slog.Attr)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var busy, log, checkpointed int
			err := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &log, &checkpointed)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				logger.Warn("wal checkpoint failed", "error", err)
				continue
			}
			if log > 0 {
				logger.Info("wal checkpoint", "log_pages", log, "checkpointed", checkpointed, "busy", busy)
				for _, hook := range hooks {
					hook("shard.checkpoint", "",
						slog.Int("log_pages", log),
						slog.Int("checkpointed", checkpointed),
						slog.Int("busy", busy))
				}
			}
		}
	}
}

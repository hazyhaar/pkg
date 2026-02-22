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
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
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

	db, err := sql.Open(cfg.driver, path)
	if err != nil {
		return nil, fmt.Errorf("dbopen: open: %w", err)
	}

	if err := applyPragmas(db, &cfg); err != nil {
		db.Close()
		return nil, err
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

func applyPragmas(db *sql.DB, cfg *config) error {
	fk := "ON"
	if !cfg.foreignKeys {
		fk = "OFF"
	}

	pragmas := []string{
		fmt.Sprintf("PRAGMA foreign_keys = %s", fk),
		"PRAGMA journal_mode = WAL",
		fmt.Sprintf("PRAGMA busy_timeout = %d", cfg.busyTimeout),
		fmt.Sprintf("PRAGMA synchronous = %s", cfg.synchronous),
	}

	if cfg.cacheSize != 0 {
		pragmas = append(pragmas, fmt.Sprintf("PRAGMA cache_size = %d", cfg.cacheSize))
	}

	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("dbopen: %s: %w", p, err)
		}
	}
	return nil
}

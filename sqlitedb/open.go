// Package sqlitedb provides the SQLite foundation for hpx.
//
// All hpx components share the same database opening conventions:
// WAL journal mode, foreign keys enabled, aggressive busy timeout.
// This package centralizes those pragmas and provides retry logic
// for SQLITE_BUSY errors.
package sqlitedb

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Option configures the database connection.
type Option func(*config)

type config struct {
	busyTimeout  int
	journalMode  string
	foreignKeys  bool
	synchronous  string
	maxOpenConns int
}

// WithBusyTimeout sets the SQLite busy_timeout in milliseconds.
// Default: 10000.
func WithBusyTimeout(ms int) Option {
	return func(c *config) { c.busyTimeout = ms }
}

// WithJournalMode sets the SQLite journal mode. Default: "wal".
func WithJournalMode(mode string) Option {
	return func(c *config) { c.journalMode = mode }
}

// WithForeignKeys enables or disables foreign key enforcement.
// Default: true.
func WithForeignKeys(enabled bool) Option {
	return func(c *config) { c.foreignKeys = enabled }
}

// WithSynchronous sets the SQLite synchronous mode. Default: "NORMAL".
func WithSynchronous(mode string) Option {
	return func(c *config) { c.synchronous = mode }
}

// WithMaxOpenConns sets the maximum number of open database connections.
// Default: 1 (serialized access, safest for WAL mode writes).
func WithMaxOpenConns(n int) Option {
	return func(c *config) { c.maxOpenConns = n }
}

// Open creates a new *sql.DB with hpx-standard pragmas applied.
//
// The default configuration enables WAL journal mode, foreign keys,
// a 10-second busy timeout, NORMAL synchronous mode, and a single
// connection (safest for SQLite writer patterns).
func Open(dsn string, opts ...Option) (*sql.DB, error) {
	cfg := config{
		busyTimeout:  10000,
		journalMode:  "wal",
		foreignKeys:  true,
		synchronous:  "NORMAL",
		maxOpenConns: 1,
	}
	for _, o := range opts {
		o(&cfg)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlitedb: open %s: %w", dsn, err)
	}

	db.SetMaxOpenConns(cfg.maxOpenConns)

	fk := "OFF"
	if cfg.foreignKeys {
		fk = "ON"
	}

	pragmas := fmt.Sprintf(`
		PRAGMA journal_mode = %s;
		PRAGMA busy_timeout = %d;
		PRAGMA foreign_keys = %s;
		PRAGMA synchronous = %s;
	`, cfg.journalMode, cfg.busyTimeout, fk, cfg.synchronous)

	if _, err := db.Exec(pragmas); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlitedb: pragmas: %w", err)
	}

	return db, nil
}

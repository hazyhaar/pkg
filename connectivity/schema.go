package connectivity

import (
	"database/sql"

	"github.com/hazyhaar/pkg/dbopen"
)

// Schema defines the routes table that drives the smart router.
// Each row maps a service name to a dispatch strategy.
//
// Strategies:
//   - "local": dispatch to an in-memory Handler registered via RegisterLocal.
//   - "quic":  dispatch via the QUIC transport factory.
//   - "http":  dispatch via the HTTP transport factory.
//   - "noop":  silently succeed without doing anything (feature flag / disable).
//
// The config column holds per-route JSON (timeouts, retry policy, etc.).
// Any UPDATE to this table automatically increments PRAGMA data_version,
// which the Watch loop detects to trigger a hot-reload.
const Schema = `
CREATE TABLE IF NOT EXISTS routes (
    service_name TEXT PRIMARY KEY,
    strategy     TEXT NOT NULL CHECK(strategy IN ('local', 'quic', 'http', 'mcp', 'dbsync', 'noop')),
    endpoint     TEXT,
    config       TEXT DEFAULT '{}',
    updated_at   INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_routes_strategy ON routes(strategy);

CREATE TRIGGER IF NOT EXISTS trg_routes_updated_at
AFTER UPDATE ON routes
FOR EACH ROW
BEGIN
    UPDATE routes SET updated_at = strftime('%s', 'now') WHERE service_name = NEW.service_name;
END;
`

// OpenDB opens a SQLite database at path with production-safe pragmas:
//   - journal_mode=WAL: concurrent reads during writes
//   - busy_timeout=5000: wait up to 5s for locks instead of immediate SQLITE_BUSY
//   - foreign_keys=ON: enforce FK constraints
//
// The caller must blank-import the SQLite driver:
//
//	import _ "modernc.org/sqlite"
//
// Use this instead of sql.Open for any database that will be shared between
// Admin writes, Router.Reload reads, and Watch polling.
func OpenDB(path string) (*sql.DB, error) {
	return dbopen.Open(path, dbopen.WithBusyTimeout(5000))
}

// Init creates the routes table if it doesn't exist.
func Init(db *sql.DB) error {
	_, err := db.Exec(Schema)
	return err
}

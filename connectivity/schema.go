package connectivity

import "database/sql"

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
    strategy     TEXT NOT NULL CHECK(strategy IN ('local', 'quic', 'http', 'grpc', 'mcp', 'noop')),
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

// Init creates the routes table if it doesn't exist.
func Init(db *sql.DB) error {
	_, err := db.Exec(Schema)
	return err
}

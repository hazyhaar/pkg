package shield

import "database/sql"

// Schema defines the SQLite tables used by shield middlewares:
//   - rate_limits: per-endpoint rate limiting rules (used by RateLimiter)
//   - maintenance: global maintenance mode flag (used by MaintenanceMode)
//
// Apply with Init(db) or execute manually. All statements are idempotent
// (CREATE IF NOT EXISTS). The BO should create these tables; dbsync replicates
// them to FO instances via FilterSpec.FullTables.
const Schema = `
CREATE TABLE IF NOT EXISTS rate_limits (
    endpoint       TEXT PRIMARY KEY,
    max_requests   INTEGER NOT NULL DEFAULT 60,
    window_seconds INTEGER NOT NULL DEFAULT 60,
    enabled        INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS maintenance (
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    active  INTEGER NOT NULL DEFAULT 0,
    message TEXT NOT NULL DEFAULT 'Maintenance en cours, veuillez patienter.'
);

INSERT OR IGNORE INTO maintenance (id, active, message)
VALUES (1, 0, 'Maintenance en cours, veuillez patienter.');
`

// Init creates the shield tables if they don't exist.
func Init(db *sql.DB) error {
	_, err := db.Exec(Schema)
	return err
}

package channels

import (
	"database/sql"

	"github.com/hazyhaar/pkg/dbopen"
)

// Schema defines the channels table that drives the bidirectional connector
// lifecycle. Each row maps a channel name to a platform and its configuration.
//
// Platforms:
//   - "whatsapp":  WhatsApp via whatsmeow (multi-device, QR pairing).
//   - "telegram":  Telegram via bot API or MTProto client.
//   - "discord":   Discord via gateway WebSocket + REST API.
//   - "signal":    Signal via signald subprocess.
//   - "webhook":   Generic inbound HTTP webhook endpoint.
//   - "matrix":    Matrix via mautrix-go client.
//
// The config column holds per-channel JSON (credentials path, device name, etc.).
// The auth_state column stores persistent authentication state (session data,
// device ID) — not raw credentials, which belong in config or env vars.
// The enabled column implements the noop pattern: set enabled=0 to shut down
// a channel without deleting its config.
//
// Any UPDATE to this table automatically increments PRAGMA data_version,
// which the Dispatcher.Watch loop detects to trigger a hot-reload.
const Schema = `
CREATE TABLE IF NOT EXISTS channels (
    name       TEXT PRIMARY KEY,
    platform   TEXT NOT NULL CHECK(platform IN ('whatsapp','telegram','discord','signal','webhook','matrix')),
    enabled    INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
    config     TEXT DEFAULT '{}',
    auth_state TEXT DEFAULT '{}',
    updated_at INTEGER NOT NULL DEFAULT (strftime('%s','now'))
);

CREATE INDEX IF NOT EXISTS idx_channels_platform ON channels(platform);

CREATE TRIGGER IF NOT EXISTS trg_channels_updated_at
AFTER UPDATE ON channels
FOR EACH ROW
BEGIN
    UPDATE channels SET updated_at = strftime('%s','now') WHERE name = NEW.name;
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
// Admin writes, Dispatcher.Reload reads, and Watch polling.
func OpenDB(path string) (*sql.DB, error) {
	return dbopen.Open(path, dbopen.WithBusyTimeout(5000))
}

// Init creates the channels table if it doesn't exist.
func Init(db *sql.DB) error {
	_, err := db.Exec(Schema)
	return err
}

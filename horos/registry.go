package horos

import (
	"database/sql"
	"fmt"
	"sync"
)

// Registry maps format IDs to human-readable names and tracks which formats
// are available. The Go-side registry is the source of truth for codec
// dispatch; the SQLite table is for observability and admin tooling.
//
// Registry is safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	formats map[uint16]FormatInfo
}

// FormatInfo describes a registered wire format.
type FormatInfo struct {
	// ID is the format identifier used in the wire envelope header.
	ID uint16

	// Name is a human-readable label (e.g. "json", "msgpack", "protobuf").
	Name string

	// MIME is the content type (e.g. "application/json", "application/msgpack").
	MIME string
}

// NewRegistry creates a registry with the built-in formats pre-registered.
func NewRegistry() *Registry {
	r := &Registry{
		formats: make(map[uint16]FormatInfo),
	}
	// Built-in: raw passthrough.
	r.formats[FormatRaw] = FormatInfo{ID: FormatRaw, Name: "raw", MIME: "application/octet-stream"}
	// Built-in: JSON.
	r.formats[FormatJSON] = FormatInfo{ID: FormatJSON, Name: "json", MIME: "application/json"}
	// Built-in: MessagePack.
	r.formats[FormatMsgp] = FormatInfo{ID: FormatMsgp, Name: "msgpack", MIME: "application/msgpack"}
	return r
}

// Register adds a format to the registry. Returns an error if the ID is
// already registered with a different name (prevents accidental collisions).
func (r *Registry) Register(info FormatInfo) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.formats[info.ID]; ok && existing.Name != info.Name {
		return fmt.Errorf("horos: format ID %d already registered as %q, cannot register as %q",
			info.ID, existing.Name, info.Name)
	}

	r.formats[info.ID] = info
	return nil
}

// Lookup returns the format info for the given ID, or false if not found.
func (r *Registry) Lookup(id uint16) (FormatInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.formats[id]
	return info, ok
}

// All returns a snapshot of all registered formats.
func (r *Registry) All() []FormatInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]FormatInfo, 0, len(r.formats))
	for _, info := range r.formats {
		out = append(out, info)
	}
	return out
}

// Schema is the SQLite DDL for the formats table. This table mirrors the
// Go-side registry for observability, admin UI, and format negotiation.
const Schema = `
CREATE TABLE IF NOT EXISTS horos_formats (
	id   INTEGER PRIMARY KEY,
	name TEXT    NOT NULL UNIQUE,
	mime TEXT    NOT NULL DEFAULT 'application/octet-stream'
);
`

// InitDB creates the formats table and seeds it with built-in formats.
func (r *Registry) InitDB(db *sql.DB) error {
	if _, err := db.Exec(Schema); err != nil {
		return fmt.Errorf("horos: create formats table: %w", err)
	}

	snapshot := r.All()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("horos: begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, info := range snapshot {
		_, err := tx.Exec(
			`INSERT OR IGNORE INTO horos_formats (id, name, mime) VALUES (?, ?, ?)`,
			info.ID, info.Name, info.MIME,
		)
		if err != nil {
			return fmt.Errorf("horos: seed format %d (%s): %w", info.ID, info.Name, err)
		}
	}

	return tx.Commit()
}

// SyncToDB writes all registered formats to the SQLite table (upsert).
// Call this after registering new formats at runtime.
func (r *Registry) SyncToDB(db *sql.DB) error {
	snapshot := r.All()

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("horos: begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, info := range snapshot {
		_, err := tx.Exec(
			`INSERT INTO horos_formats (id, name, mime) VALUES (?, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET name=excluded.name, mime=excluded.mime`,
			info.ID, info.Name, info.MIME,
		)
		if err != nil {
			return fmt.Errorf("horos: sync format %d (%s): %w", info.ID, info.Name, err)
		}
	}

	return tx.Commit()
}

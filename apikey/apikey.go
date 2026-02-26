// CLAUDE:SUMMARY API key lifecycle: generate, resolve, revoke, list. SHA-256 hashed storage, service-scoped, rate-limited.
// CLAUDE:DEPENDS modernc.org/sqlite, github.com/hazyhaar/pkg/trace
// CLAUDE:EXPORTS Store, Key, Generate, Resolve, Revoke, List
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	_ "github.com/hazyhaar/pkg/trace" // registers "sqlite-trace" driver
)

// Prefix for all horoskeys.
const Prefix = "hk_"

// Key represents an API key record in the database.
type Key struct {
	ID        string   `json:"id"`
	Prefix    string   `json:"prefix"`     // first 8 chars of the clear key (for identification)
	Hash      string   `json:"-"`          // SHA-256 of the full clear key — never exposed
	OwnerID   string   `json:"owner_id"`   // user_id of the key owner (for billing)
	Name      string   `json:"name"`       // human label ("Mon LLM Claude", "Script backup")
	Services  []string `json:"services"`   // authorized services ["sas_ingester", "veille"]
	RateLimit int      `json:"rate_limit"` // requests per minute (0 = unlimited)
	CreatedAt string   `json:"created_at"`
	ExpiresAt string   `json:"expires_at,omitempty"` // empty = never expires
	RevokedAt string   `json:"revoked_at,omitempty"` // non-empty = revoked
}

// Store wraps an SQLite database for API key management.
type Store struct {
	db *sql.DB
}

// OpenStore opens (or creates) the SQLite database at path and runs migrations.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite-trace", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// OpenStoreWithDB wraps an existing *sql.DB (e.g. shared with another service).
// Runs migrations on the provided DB.
func OpenStoreWithDB(db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// DB returns the underlying *sql.DB.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the underlying database connection.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	const ddl = `
CREATE TABLE IF NOT EXISTS api_keys (
    id          TEXT PRIMARY KEY,
    prefix      TEXT NOT NULL,
    hash        TEXT NOT NULL UNIQUE,
    owner_id    TEXT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    services    TEXT NOT NULL DEFAULT '[]',
    rate_limit  INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL DEFAULT '',
    revoked_at  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_api_keys_hash     ON api_keys(hash);
CREATE INDEX IF NOT EXISTS idx_api_keys_owner    ON api_keys(owner_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_prefix   ON api_keys(prefix);
`
	_, err := s.db.Exec(ddl)
	return err
}

// Generate creates a new API key, stores its hash, and returns the clear key
// exactly once. The clear key is never stored — only its SHA-256 hash.
//
// Format: "hk_" + 32 random bytes hex = 67 chars total.
// Prefix stored: first 8 chars ("hk_7f3a9") for identification without exposure.
func (s *Store) Generate(id, ownerID, name string, services []string, rateLimit int) (clearKey string, key *Key, err error) {
	if ownerID == "" {
		return "", nil, fmt.Errorf("owner_id is required")
	}
	if id == "" {
		return "", nil, fmt.Errorf("id is required")
	}

	// Generate 32 random bytes → 64 hex chars.
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", nil, fmt.Errorf("generate random: %w", err)
	}
	clearKey = Prefix + hex.EncodeToString(raw[:])

	// Hash for storage.
	hash := hashKey(clearKey)

	// Prefix for identification.
	prefix := clearKey[:8] // "hk_7f3a9"

	// Serialize services.
	svcJSON := "[]"
	if len(services) > 0 {
		svcJSON = `["` + strings.Join(services, `","`) + `"]`
	}

	now := time.Now().UTC().Format(time.RFC3339)

	key = &Key{
		ID:        id,
		Prefix:    prefix,
		Hash:      hash,
		OwnerID:   ownerID,
		Name:      name,
		Services:  services,
		RateLimit: rateLimit,
		CreatedAt: now,
	}

	_, err = s.db.Exec(
		`INSERT INTO api_keys (id, prefix, hash, owner_id, name, services, rate_limit, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		key.ID, key.Prefix, key.Hash, key.OwnerID, key.Name, svcJSON, key.RateLimit, key.CreatedAt,
	)
	if err != nil {
		return "", nil, fmt.Errorf("insert key: %w", err)
	}

	return clearKey, key, nil
}

// Resolve validates a clear API key and returns the associated Key record.
// Returns an error if the key is invalid, expired, or revoked.
func (s *Store) Resolve(clearKey string) (*Key, error) {
	if !strings.HasPrefix(clearKey, Prefix) {
		return nil, fmt.Errorf("invalid key format: must start with %q", Prefix)
	}

	hash := hashKey(clearKey)

	var k Key
	var svcJSON string
	err := s.db.QueryRow(
		`SELECT id, prefix, hash, owner_id, name, services, rate_limit, created_at, expires_at, revoked_at
		 FROM api_keys WHERE hash = ?`, hash,
	).Scan(&k.ID, &k.Prefix, &k.Hash, &k.OwnerID, &k.Name, &svcJSON, &k.RateLimit, &k.CreatedAt, &k.ExpiresAt, &k.RevokedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("unknown key")
	}
	if err != nil {
		return nil, fmt.Errorf("lookup: %w", err)
	}

	// Check revoked.
	if k.RevokedAt != "" {
		return nil, fmt.Errorf("key revoked at %s", k.RevokedAt)
	}

	// Check expired.
	if k.ExpiresAt != "" {
		exp, err := time.Parse(time.RFC3339, k.ExpiresAt)
		if err == nil && time.Now().UTC().After(exp) {
			return nil, fmt.Errorf("key expired at %s", k.ExpiresAt)
		}
	}

	// Parse services.
	k.Services = parseServices(svcJSON)

	return &k, nil
}

// Revoke marks an API key as revoked. It can no longer be used for authentication.
func (s *Store) Revoke(keyID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`UPDATE api_keys SET revoked_at = ? WHERE id = ? AND revoked_at = ''`, now, keyID)
	if err != nil {
		return fmt.Errorf("revoke: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("key not found or already revoked: %s", keyID)
	}
	return nil
}

// List returns all API keys for an owner (excluding the hash).
func (s *Store) List(ownerID string) ([]*Key, error) {
	rows, err := s.db.Query(
		`SELECT id, prefix, owner_id, name, services, rate_limit, created_at, expires_at, revoked_at
		 FROM api_keys WHERE owner_id = ? ORDER BY created_at DESC, rowid DESC`, ownerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*Key
	for rows.Next() {
		var k Key
		var svcJSON string
		if err := rows.Scan(&k.ID, &k.Prefix, &k.OwnerID, &k.Name, &svcJSON, &k.RateLimit, &k.CreatedAt, &k.ExpiresAt, &k.RevokedAt); err != nil {
			return nil, err
		}
		k.Services = parseServices(svcJSON)
		keys = append(keys, &k)
	}
	return keys, rows.Err()
}

// SetExpiry sets or clears the expiration date for a key.
func (s *Store) SetExpiry(keyID string, expiresAt string) error {
	_, err := s.db.Exec(`UPDATE api_keys SET expires_at = ? WHERE id = ?`, expiresAt, keyID)
	return err
}

// UpdateServices updates the authorized services for a key (no key rotation needed).
func (s *Store) UpdateServices(keyID string, services []string) error {
	svcJSON := "[]"
	if len(services) > 0 {
		svcJSON = `["` + strings.Join(services, `","`) + `"]`
	}
	_, err := s.db.Exec(`UPDATE api_keys SET services = ? WHERE id = ?`, svcJSON, keyID)
	return err
}

// HasService checks if a resolved key is authorized for a given service.
func (k *Key) HasService(service string) bool {
	if len(k.Services) == 0 {
		return true // no restrictions = all services
	}
	for _, s := range k.Services {
		if s == service {
			return true
		}
	}
	return false
}

// hashKey returns the SHA-256 hex digest of a clear key.
func hashKey(clearKey string) string {
	h := sha256.Sum256([]byte(clearKey))
	return hex.EncodeToString(h[:])
}

// parseServices parses a JSON array string like `["a","b"]` into a slice.
func parseServices(s string) []string {
	if s == "" || s == "[]" {
		return nil
	}
	// Simple parser for ["a","b","c"] — no external JSON dependency needed.
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"`)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

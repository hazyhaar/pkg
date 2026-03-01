// CLAUDE:SUMMARY API key lifecycle: generate, resolve, revoke, list. SHA-256 hashed storage, service-scoped, dossier-scoped, rate-limited.
// CLAUDE:DEPENDS modernc.org/sqlite, github.com/hazyhaar/pkg/trace
// CLAUDE:EXPORTS Store, Key, Generate, Resolve, Revoke, List, ListByDossier, Count, Option, WithDossier, StoreOption, WithMaxKeys, WithAudit, AuditFunc
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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
	DossierID string   `json:"dossier_id,omitempty"` // scoped dossier (empty = legacy/wildcard)
	CreatedAt string   `json:"created_at"`
	ExpiresAt string   `json:"expires_at,omitempty"` // empty = never expires
	RevokedAt string   `json:"revoked_at,omitempty"` // non-empty = revoked
}

// IsDossierScoped returns true if this key is bound to a specific dossier.
func (k *Key) IsDossierScoped() bool { return k.DossierID != "" }

// Option configures optional parameters for Generate.
type Option func(*generateOpts)

type generateOpts struct {
	dossierID string
}

// WithDossier binds the generated key to a specific dossier.
func WithDossier(dossierID string) Option {
	return func(o *generateOpts) { o.dossierID = dossierID }
}

// AuditFunc is called after successful key operations with the event name,
// key ID, and owner ID. For Revoke, ownerID is empty (not looked up).
type AuditFunc func(event, keyID, ownerID string)

// StoreOption configures store-level behavior. Distinct from Option (which
// configures Generate).
type StoreOption func(*Store)

// WithMaxKeys sets the maximum number of non-revoked keys per owner.
// 0 means unlimited (default).
func WithMaxKeys(n int) StoreOption {
	return func(s *Store) { s.maxKeys = n }
}

// WithAudit registers a hook called after successful Generate, Resolve, and
// Revoke operations.
func WithAudit(fn AuditFunc) StoreOption {
	return func(s *Store) { s.auditFn = fn }
}

// Store wraps an SQLite database for API key management.
type Store struct {
	db      *sql.DB
	owned   bool      // true if we opened the DB (OpenStore), false if shared (OpenStoreWithDB)
	maxKeys int       // max non-revoked keys per owner; 0 = unlimited
	auditFn AuditFunc // optional audit hook
}

// OpenStore opens (or creates) the SQLite database at path and runs migrations.
func OpenStore(path string, opts ...StoreOption) (*Store, error) {
	db, err := sql.Open("sqlite-trace", path+"?_txlock=immediate&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	s := &Store{db: db, owned: true}
	for _, o := range opts {
		o(s)
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// OpenStoreWithDB wraps an existing *sql.DB (e.g. shared with another service).
// Runs migrations on the provided DB. Close() on the returned Store is a no-op
// to avoid closing the shared DB.
func OpenStoreWithDB(db *sql.DB, opts ...StoreOption) (*Store, error) {
	s := &Store{db: db, owned: false}
	for _, o := range opts {
		o(s)
	}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// DB returns the underlying *sql.DB.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the underlying database connection. If the store was created
// via OpenStoreWithDB (shared DB), Close is a no-op to avoid breaking other
// consumers of the same *sql.DB.
func (s *Store) Close() error {
	if !s.owned {
		return nil
	}
	return s.db.Close()
}

// Count returns the number of non-revoked keys for the given owner.
func (s *Store) Count(ownerID string) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM api_keys WHERE owner_id = ? AND revoked_at = ''`,
		ownerID,
	).Scan(&n)
	return n, err
}

// audit calls the audit hook if one was registered via WithAudit.
func (s *Store) audit(event, keyID, ownerID string) {
	if s.auditFn != nil {
		s.auditFn(event, keyID, ownerID)
	}
}

func (s *Store) migrate() error {
	// NOTE: pragmas (foreign_keys, WAL, busy_timeout, synchronous) are set via
	// DSN _pragma= parameters, NOT via db.Exec. db.Exec only touches one
	// connection in the pool and is unreliable with sql.DB connection pooling.
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
	if _, err := s.db.Exec(ddl); err != nil {
		return err
	}

	// Migration: add dossier_id column (idempotent — ignore "duplicate column" error).
	if _, err := s.db.Exec(`ALTER TABLE api_keys ADD COLUMN dossier_id TEXT NOT NULL DEFAULT ''`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("add dossier_id column: %w", err)
		}
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_api_keys_dossier ON api_keys(dossier_id)`); err != nil {
		return fmt.Errorf("create dossier index: %w", err)
	}

	return nil
}

// Generate creates a new API key, stores its hash, and returns the clear key
// exactly once. The clear key is never stored — only its SHA-256 hash.
//
// Format: "hk_" + 32 random bytes hex = 67 chars total.
// Prefix stored: first 8 chars ("hk_7f3a9") for identification without exposure.
func (s *Store) Generate(id, ownerID, name string, services []string, rateLimit int, opts ...Option) (clearKey string, key *Key, err error) {
	if ownerID == "" {
		return "", nil, fmt.Errorf("owner_id is required")
	}
	if id == "" {
		return "", nil, fmt.Errorf("id is required")
	}

	// Enforce per-user key limit if configured.
	if s.maxKeys > 0 {
		var n int
		n, err = s.Count(ownerID)
		if err != nil {
			return "", nil, fmt.Errorf("count keys: %w", err)
		}
		if n >= s.maxKeys {
			return "", nil, fmt.Errorf("key limit reached: owner %s has %d keys (limit %d)", ownerID, n, s.maxKeys)
		}
	}

	var o generateOpts
	for _, fn := range opts {
		fn(&o)
	}

	// Generate 32 random bytes → 64 hex chars.
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return "", nil, fmt.Errorf("generate random: %w", err)
	}
	clearKey = Prefix + hex.EncodeToString(raw[:])

	// Hash for storage.
	hash := hashKey(clearKey)

	// Prefix for identification.
	prefix := clearKey[:8] // "hk_7f3a9"

	// Serialize services via encoding/json for safe escaping.
	svcJSON, err := marshalServices(services)
	if err != nil {
		return "", nil, fmt.Errorf("marshal services: %w", err)
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
		DossierID: o.dossierID,
		CreatedAt: now,
	}

	_, err = s.db.Exec(
		`INSERT INTO api_keys (id, prefix, hash, owner_id, name, services, rate_limit, dossier_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		key.ID, key.Prefix, key.Hash, key.OwnerID, key.Name, svcJSON, key.RateLimit, key.DossierID, key.CreatedAt,
	)
	if err != nil {
		return "", nil, fmt.Errorf("insert key: %w", err)
	}

	s.audit("generate", key.ID, key.OwnerID)
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
		`SELECT id, prefix, hash, owner_id, name, services, rate_limit, dossier_id, created_at, expires_at, revoked_at
		 FROM api_keys WHERE hash = ?`, hash,
	).Scan(&k.ID, &k.Prefix, &k.Hash, &k.OwnerID, &k.Name, &svcJSON, &k.RateLimit, &k.DossierID, &k.CreatedAt, &k.ExpiresAt, &k.RevokedAt)
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

	s.audit("resolve", k.ID, k.OwnerID)
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
	s.audit("revoke", keyID, "")
	return nil
}

// List returns all API keys for an owner (excluding the hash).
func (s *Store) List(ownerID string) ([]*Key, error) {
	rows, err := s.db.Query(
		`SELECT id, prefix, owner_id, name, services, rate_limit, dossier_id, created_at, expires_at, revoked_at
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
		if err := rows.Scan(&k.ID, &k.Prefix, &k.OwnerID, &k.Name, &svcJSON, &k.RateLimit, &k.DossierID, &k.CreatedAt, &k.ExpiresAt, &k.RevokedAt); err != nil {
			return nil, err
		}
		k.Services = parseServices(svcJSON)
		keys = append(keys, &k)
	}
	return keys, rows.Err()
}

// ListByDossier returns all active (non-revoked) keys scoped to a specific dossier.
func (s *Store) ListByDossier(dossierID string) ([]*Key, error) {
	rows, err := s.db.Query(
		`SELECT id, prefix, owner_id, name, services, rate_limit, dossier_id, created_at, expires_at, revoked_at
		 FROM api_keys WHERE dossier_id = ? AND revoked_at = '' ORDER BY created_at DESC, rowid DESC`, dossierID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []*Key
	for rows.Next() {
		var k Key
		var svcJSON string
		if err := rows.Scan(&k.ID, &k.Prefix, &k.OwnerID, &k.Name, &svcJSON, &k.RateLimit, &k.DossierID, &k.CreatedAt, &k.ExpiresAt, &k.RevokedAt); err != nil {
			return nil, err
		}
		k.Services = parseServices(svcJSON)
		keys = append(keys, &k)
	}
	return keys, rows.Err()
}

// SetExpiry sets or clears the expiration date for a key.
// Returns an error if the key does not exist or is revoked.
func (s *Store) SetExpiry(keyID string, expiresAt string) error {
	res, err := s.db.Exec(`UPDATE api_keys SET expires_at = ? WHERE id = ? AND revoked_at = ''`, expiresAt, keyID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("key not found or revoked: %s", keyID)
	}
	return nil
}

// UpdateServices updates the authorized services for a key (no key rotation needed).
// Returns an error if the key does not exist or is revoked.
func (s *Store) UpdateServices(keyID string, services []string) error {
	svcJSON, err := marshalServices(services)
	if err != nil {
		return fmt.Errorf("marshal services: %w", err)
	}
	res, err := s.db.Exec(`UPDATE api_keys SET services = ? WHERE id = ? AND revoked_at = ''`, svcJSON, keyID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("key not found or revoked: %s", keyID)
	}
	return nil
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

// marshalServices serializes a service list to JSON using encoding/json.
func marshalServices(services []string) (string, error) {
	if len(services) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(services)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// parseServices parses a JSON array string like `["a","b"]` into a slice.
func parseServices(s string) []string {
	if s == "" || s == "[]" {
		return nil
	}
	var result []string
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return nil
	}
	return result
}

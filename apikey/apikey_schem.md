╔══════════════════════════════════════════════════════════════════════════════╗
║  apikey — API key lifecycle: generate, resolve, revoke. SHA-256 hashed.    ║
╠══════════════════════════════════════════════════════════════════════════════╣
║                                                                            ║
║  FLOW                                                                      ║
║  ────                                                                      ║
║                                                                            ║
║  ┌─────────────┐     Generate()      ┌──────────────┐                      ║
║  │ Caller      │ ──────────────────→ │ Store        │                      ║
║  │ (id,owner,  │                     │              │                      ║
║  │  name,svcs, │  crypto/rand 32B    │ ┌──────────┐ │    INSERT            ║
║  │  rateLimit, │ ──→ "hk_" + hex  ──→│ │ SHA-256  │ │ ──→ api_keys        ║
║  │  opts...)   │      (67 chars)     │ │ hash     │ │                      ║
║  └─────────────┘                     │ └──────────┘ │                      ║
║       │                              └──────────────┘                      ║
║       │ returns (clearKey, *Key, err)                                      ║
║       │ clearKey returned ONCE, never stored                               ║
║       ▼                                                                    ║
║  ┌─────────────┐     Resolve()       ┌──────────────┐                      ║
║  │ Auth layer  │ ──────────────────→ │ Store        │                      ║
║  │ (clearKey)  │                     │              │   SELECT WHERE       ║
║  │             │  SHA-256(clearKey)   │ ┌──────────┐ │   hash = ?          ║
║  │             │ ──────────────────→ │ │ Lookup   │ │ ──→ api_keys        ║
║  └─────────────┘                     │ │ + check  │ │                      ║
║       │                              │ │ revoked  │ │                      ║
║       │ returns (*Key, err)          │ │ + check  │ │                      ║
║       │ err if revoked/expired       │ │ expired  │ │                      ║
║       ▼                              │ └──────────┘ │                      ║
║  key.HasService("sas_ingester")      └──────────────┘                      ║
║  key.IsDossierScoped()                                                     ║
║                                                                            ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  DATABASE TABLE: api_keys                                                  ║
║  ─────────────────────────                                                 ║
║  id          TEXT PK          -- caller-supplied UUID                       ║
║  prefix      TEXT NOT NULL    -- first 8 chars of clear key ("hk_7f3a")    ║
║  hash        TEXT NOT NULL UQ -- SHA-256 hex of full clear key             ║
║  owner_id    TEXT NOT NULL    -- user who owns this key                     ║
║  name        TEXT             -- human label                               ║
║  services    TEXT DEFAULT '[]'-- JSON array: ["sas_ingester","veille"]      ║
║  rate_limit  INTEGER          -- req/min, 0 = unlimited                    ║
║  dossier_id  TEXT DEFAULT ''  -- scoped dossier (empty = wildcard)          ║
║  created_at  TEXT RFC3339     -- creation timestamp                        ║
║  expires_at  TEXT             -- empty = never expires                      ║
║  revoked_at  TEXT             -- non-empty = revoked (irreversible)         ║
║                                                                            ║
║  Indexes: hash, owner_id, prefix, dossier_id                              ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  KEY FORMAT                                                                ║
║  ──────────                                                                ║
║  "hk_" + 32 random bytes hex = 67 chars total                             ║
║  Prefix stored: "hk_7f3a9" (8 chars) — visible, for identification        ║
║  Hash stored: SHA-256 hex — never exposed via JSON                         ║
║  Clear key: returned once by Generate(), never persisted                   ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED TYPES                                                            ║
║  ──────────────                                                            ║
║  Store        struct { db *sql.DB }                                        ║
║  Key          struct { ID, Prefix, Hash, OwnerID, Name string;             ║
║                        Services []string; RateLimit int;                   ║
║                        DossierID, CreatedAt, ExpiresAt, RevokedAt string } ║
║  Option       func(*generateOpts)                                          ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED FUNCTIONS                                                        ║
║  ──────────────────                                                        ║
║  OpenStore(path string) (*Store, error)                                    ║
║      -- opens/creates SQLite DB at path, runs migration                    ║
║  OpenStoreWithDB(db *sql.DB) (*Store, error)                               ║
║      -- wraps existing DB, runs migration                                  ║
║  (s *Store) Generate(id, ownerID, name string, svcs []string,              ║
║                       rateLimit int, opts ...Option) (string, *Key, error) ║
║  (s *Store) Resolve(clearKey string) (*Key, error)                         ║
║  (s *Store) Revoke(keyID string) error                                     ║
║  (s *Store) List(ownerID string) ([]*Key, error)                           ║
║  (s *Store) ListByDossier(dossierID string) ([]*Key, error)                ║
║  (s *Store) SetExpiry(keyID, expiresAt string) error                       ║
║  (s *Store) UpdateServices(keyID string, svcs []string) error              ║
║  (s *Store) DB() *sql.DB                                                   ║
║  (s *Store) Close() error                                                  ║
║  (k *Key) HasService(svc string) bool   -- empty svcs = wildcard (true)    ║
║  (k *Key) IsDossierScoped() bool        -- DossierID != ""                 ║
║  WithDossier(dossierID string) Option                                      ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES (internal)                                                   ║
║  ────────────────────────                                                  ║
║  github.com/hazyhaar/pkg/trace   -- registers "sqlite-trace" SQL driver    ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  INVARIANTS                                                                ║
║  ──────────                                                                ║
║  - Clear key NEVER stored — only SHA-256 hash persisted                    ║
║  - Resolve checks revocation THEN expiration (order matters)               ║
║  - Services=[] or nil = wildcard (access to ALL services)                  ║
║  - DossierID="" = legacy key (all dossiers), != "" = scoped to one         ║
║  - Double revocation returns error (not idempotent by design)              ║
║  - crypto/rand only (never math/rand)                                      ║
║  - DSN uses _pragma= for WAL+busy_timeout (standard HOROS pattern)         ║
╚══════════════════════════════════════════════════════════════════════════════╝

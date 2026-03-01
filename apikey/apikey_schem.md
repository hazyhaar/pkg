╔══════════════════════════════════════════════════════════════════════════════════╗
║  apikey — API key lifecycle: generate, resolve, revoke. SHA-256 hashed.        ║
║  Module: github.com/hazyhaar/pkg/apikey    Fichier: apikey.go (~360 LOC)       ║
╠══════════════════════════════════════════════════════════════════════════════════╣
║                                                                                ║
║  GENERATE FLOW                                                                 ║
║  ─────────────                                                                 ║
║                                                                                ║
║  ┌─────────────┐     Generate()      ┌──────────────────────────┐              ║
║  │ Caller      │ ──────────────────▶ │ Store                    │              ║
║  │ (id,owner,  │                     │                          │              ║
║  │  name,svcs, │  crypto/rand 32B    │ ┌──────────┐            │   INSERT     ║
║  │  rateLimit, │ ─▶ "hk_" + hex  ──▶│ │ SHA-256  │ ──────────▶│ ──▶ api_keys ║
║  │  opts...)   │     (67 chars)      │ │ hash     │            │              ║
║  └─────────────┘                     │ └──────────┘            │              ║
║       │                              │                          │              ║
║       │ returns (clearKey, *Key, err)│ encoding/json.Marshal    │              ║
║       │ clearKey returned ONCE       │ for services []string    │              ║
║       ▼                              └──────────────────────────┘              ║
║                                                                                ║
║  RESOLVE FLOW                                                                  ║
║  ────────────                                                                  ║
║                                                                                ║
║  ┌─────────────┐     Resolve()       ┌──────────────────────────┐              ║
║  │ Auth layer  │ ──────────────────▶ │ Store                    │              ║
║  │ (clearKey)  │                     │                          │              ║
║  │             │  1. HasPrefix("hk_")│ 2. SHA-256(clearKey)     │              ║
║  │             │  ──────────────────▶│ ──▶ SELECT WHERE hash=?  │              ║
║  └─────────────┘                     │ 3. Check revoked_at!=""  │              ║
║       │                              │ 4. Check expires_at past │              ║
║       │ returns (*Key, err)          │ 5. json.Unmarshal(svcs)  │              ║
║       │ err if revoked/expired       └──────────────────────────┘              ║
║       ▼                                                                        ║
║  key.HasService("sas_ingester")   ◀── nil svcs = wildcard (true)               ║
║  key.IsDossierScoped()            ◀── DossierID != ""                          ║
║                                                                                ║
║  MUTATION FLOW                                                                 ║
║  ─────────────                                                                 ║
║                                                                                ║
║  Revoke(keyID) ──▶ UPDATE SET revoked_at WHERE revoked_at='' ──▶ irreversible  ║
║  SetExpiry(keyID, t) ──▶ UPDATE WHERE revoked_at='' ──▶ err if revoked/absent  ║
║  UpdateServices(keyID, svcs) ──▶ idem ──▶ err if revoked/absent               ║
║                                                                                ║
╠══════════════════════════════════════════════════════════════════════════════════╣
║  DATABASE TABLE: api_keys                                                      ║
║  ─────────────────────────                                                     ║
║                                                                                ║
║  id          TEXT PK          -- caller-supplied UUID (ex: "key_019...")        ║
║  prefix      TEXT NOT NULL    -- first 8 chars ("hk_7f3a9")                    ║
║  hash        TEXT NOT NULL UQ -- SHA-256 hex, 64 chars, json:"-"               ║
║  owner_id    TEXT NOT NULL    -- user_id of key owner                           ║
║  name        TEXT NOT NULL    -- human label, default ''                        ║
║  services    TEXT NOT NULL    -- JSON array via encoding/json, default '[]'     ║
║  rate_limit  INTEGER NOT NULL -- req/min, 0 = unlimited, default 0             ║
║  dossier_id  TEXT NOT NULL    -- scoped dossier, '' = legacy/wildcard          ║
║  created_at  TEXT NOT NULL    -- RFC3339                                        ║
║  expires_at  TEXT NOT NULL    -- '' = never expires                             ║
║  revoked_at  TEXT NOT NULL    -- '' = active, non-empty = revoked              ║
║                                                                                ║
║  Indexes: hash (UQ), owner_id, prefix, dossier_id                             ║
║  Migration: dossier_id added via ALTER TABLE (idempotent)                      ║
║                                                                                ║
╠══════════════════════════════════════════════════════════════════════════════════╣
║  KEY FORMAT                                                                    ║
║  ──────────                                                                    ║
║  "hk_" + 32 random bytes hex = 67 chars total                                 ║
║  Entropy: 256 bits (crypto/rand)                                               ║
║  Prefix stored: clearKey[:8] = "hk_" + 5 hex = 20 bits                        ║
║  Hash stored: SHA-256 hex = 64 chars (never exposed via JSON)                  ║
║  Clear key: returned ONCE by Generate(), NEVER persisted                       ║
║                                                                                ║
╠══════════════════════════════════════════════════════════════════════════════════╣
║  CONSUMERS IN ECOSYSTEM                                                        ║
║  ──────────────────────                                                        ║
║                                                                                ║
║  siftrag:                                                                      ║
║    cmd/siftrag/main.go    ──▶ OpenStoreWithDB(db) on shared siftrag.db         ║
║    middleware/apikey.go    ──▶ APIKeyAuth: X-API-Key ──▶ Resolve ──▶ ctx       ║
║                               MultiKeyAuth: X-API-Keys (comma-sep)             ║
║    handlers/apikey.go     ──▶ CRUD: List, Create, Revoke, Regenerate           ║
║    services/dossier.go    ──▶ VerifyAccess(dossierID, userID, key)             ║
║                                                                                ║
║  sas_ingester:                                                                 ║
║    WithKeyResolver(func)  ──▶ Resolve + HasService("sas_ingester")             ║
║                                                                                ║
╠══════════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED TYPES                                                                ║
║  ──────────────                                                                ║
║  Store        struct { db *sql.DB }                                            ║
║  Key          struct { ID, Prefix, Hash, OwnerID, Name string;                 ║
║                        Services []string; RateLimit int;                       ║
║                        DossierID, CreatedAt, ExpiresAt, RevokedAt string }     ║
║  Option       func(*generateOpts)                                              ║
║                                                                                ║
╠══════════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED FUNCTIONS                                                            ║
║  ──────────────────                                                            ║
║  OpenStore(path string) (*Store, error)                                        ║
║  OpenStoreWithDB(db *sql.DB) (*Store, error)  -- ATTENTION: Close() ferme db   ║
║  (s) Generate(id, ownerID, name, svcs, rate, opts...) (string, *Key, error)    ║
║  (s) Resolve(clearKey string) (*Key, error)                                    ║
║  (s) Revoke(keyID string) error                                                ║
║  (s) List(ownerID string) ([]*Key, error)                                      ║
║  (s) ListByDossier(dossierID string) ([]*Key, error)                           ║
║  (s) SetExpiry(keyID, expiresAt string) error    -- refuse si revoque/absent   ║
║  (s) UpdateServices(keyID, svcs []string) error  -- refuse si revoque/absent   ║
║  (s) DB() *sql.DB                                                              ║
║  (s) Close() error                                                             ║
║  (k) HasService(svc string) bool   -- nil svcs = wildcard (true)               ║
║  (k) IsDossierScoped() bool        -- DossierID != ""                          ║
║  WithDossier(dossierID string) Option                                          ║
║                                                                                ║
╠══════════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES                                                                  ║
║  ────────────                                                                  ║
║  github.com/hazyhaar/pkg/trace   -- registers "sqlite-trace" SQL driver        ║
║  encoding/json                   -- services serialization (safe escaping)      ║
║  crypto/rand, crypto/sha256      -- key generation + hashing                   ║
║                                                                                ║
╠══════════════════════════════════════════════════════════════════════════════════╣
║  INVARIANTS                                                                    ║
║  ──────────                                                                    ║
║  - Clear key NEVER stored — only SHA-256 hash persisted                        ║
║  - Resolve: check revoked THEN expired (order matters)                         ║
║  - Services=[] or nil = wildcard (access to ALL services)                      ║
║  - DossierID="" = legacy key, != "" = scoped to one dossier                    ║
║  - Double revocation = error (not idempotent)                                  ║
║  - SetExpiry/UpdateServices refuse on revoked or non-existent key              ║
║  - Services serialized via encoding/json (not string concat)                   ║
║  - Pragmas via DSN _pragma= only (never db.Exec("PRAGMA"))                    ║
║  - crypto/rand only (never math/rand)                                          ║
║                                                                                ║
╠══════════════════════════════════════════════════════════════════════════════════╣
║  ARCHITECTURAL FLAWS (audit 2026-03-01)                                        ║
║  ──────────────────────────────────────                                        ║
║                                                                                ║
║  [CRIT] IDOR on revoke — siftrag handler doesn't check key ownership          ║
║  [HIGH] rate_limit stored but never enforced — dead feature                    ║
║  [HIGH] clear key in flash cookie — logged by proxies, lost if missed          ║
║  [HIGH] dossier delete doesn't revoke scoped keys — orphan keys remain valid   ║
║  [MED]  MultiKeyAuth no cross-check dossier scope — isolation bypass           ║
║  [MED]  no audit trail — create/revoke/resolve not logged                      ║
║  [LOW]  no per-user key count limit                                            ║
║  [LOW]  Close() on shared DB kills other consumers                             ║
║                                                                                ║
║  Details: see CLAUDE.md § "Failles architecturales connues"                    ║
║                                                                                ║
╠══════════════════════════════════════════════════════════════════════════════════╣
║  TESTS (28 total)                                                              ║
║  ────────────────                                                              ║
║  16 existing: Generate, Resolve (valid/invalid/unknown), Revoke (+ double),    ║
║     Expiry (past/future), List, UpdateServices, HasService, HashKey,           ║
║     ParseServices, WithDossier, WithoutDossier, IsDossierScoped,               ║
║     ListByDossier, List_IncludesDossierID                                      ║
║  12 audit: ServiceNameWithQuote, ServiceNameWithComma, DuplicateID,            ║
║     SetExpiryNonExistent, UpdateServicesNonExistent, SetExpiryOnRevoked,       ║
║     UpdateServicesOnRevoked, EmptyID, EmptyOwner, MigrateIdempotent,           ║
║     OpenStoreWithDB, PragmaExecRemoved                                         ║
║                                                                                ║
╚══════════════════════════════════════════════════════════════════════════════════╝

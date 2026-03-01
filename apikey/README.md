# apikey

API key lifecycle for the HOROS ecosystem — generate, resolve, revoke, expire.

Keys are SHA-256 hashed before storage. The clear key is returned exactly once
on creation and never persisted. Service scoping and dossier scoping restrict
what a key can access.

## Install

```go
import "github.com/hazyhaar/pkg/apikey"
```

## Quick start

```go
// Open a dedicated store.
store, err := apikey.OpenStore("/data/keys.db")

// Or share an existing database.
store, err := apikey.OpenStoreWithDB(existingDB)

// Generate — clear key returned once, never stored.
clearKey, key, err := store.Generate(
    "key_"+uuid.Must(uuid.NewV7()).String(),
    userID,
    "My automation key",
    []string{"sas_ingester"},  // nil = all services
    60,                         // rate limit (req/min), 0 = unlimited
    apikey.WithDossier(dossierID), // optional: scope to one dossier
)
// Save clearKey now — it won't be available again.

// Resolve — validate and get metadata.
key, err := store.Resolve(clearKey)
if err != nil {
    // invalid format, unknown, revoked, or expired
}
if !key.HasService("sas_ingester") {
    // unauthorized for this service
}
if key.IsDossierScoped() && key.DossierID != targetDossier {
    // wrong dossier
}

// Revoke — irreversible.
err = store.Revoke(keyID)

// Lifecycle operations (refuse on revoked/non-existent keys).
err = store.SetExpiry(keyID, time.Now().Add(30*24*time.Hour).Format(time.RFC3339))
err = store.UpdateServices(keyID, []string{"sas_ingester", "veille"})

// List.
keys, err := store.List(ownerID)
keys, err := store.ListByDossier(dossierID)
```

## Key format

```
hk_7f3a9b2c1d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a
^^^
prefix "hk_" + 64 hex chars (32 random bytes) = 67 chars total
```

- **Prefix** (`hk_7f3a9`): first 8 chars stored for visual identification
- **Hash**: SHA-256 hex of the full clear key stored in DB
- **Clear key**: returned once by `Generate()`, never persisted
- **Entropy**: 256 bits via `crypto/rand`

## Database schema

Single table `api_keys` with 11 columns. Auto-migrated on `OpenStore`/`OpenStoreWithDB`.

```sql
CREATE TABLE api_keys (
    id          TEXT PRIMARY KEY,
    prefix      TEXT NOT NULL,
    hash        TEXT NOT NULL UNIQUE,
    owner_id    TEXT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    services    TEXT NOT NULL DEFAULT '[]',
    rate_limit  INTEGER NOT NULL DEFAULT 0,
    dossier_id  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL DEFAULT '',
    revoked_at  TEXT NOT NULL DEFAULT ''
);
```

Indexes: `hash` (unique), `owner_id`, `prefix`, `dossier_id`.

## Service scoping

`Services` is a JSON array of authorized service names. An empty or nil list
means **wildcard** — the key has access to all services.

```go
key.HasService("sas_ingester") // true if services contains it or is empty
```

## Dossier scoping

Keys can be bound to a single dossier via `WithDossier(dossierID)`.

```go
key.IsDossierScoped()  // true if DossierID != ""
key.DossierID          // the bound dossier, or "" for legacy/wildcard
```

Legacy keys (`DossierID = ""`) have access to all dossiers owned by their owner.

## Resolve guarantees

`Resolve(clearKey)` checks in order:
1. Format validation (`hk_` prefix)
2. Hash lookup (SHA-256 → single row)
3. Revocation check (`revoked_at != ""` → error)
4. Expiration check (`expires_at` in the past → error)
5. Service deserialization (`json.Unmarshal`)

## Test

```bash
CGO_ENABLED=0 go test -v -count=1 ./apikey/...
```

28 tests including 12 audit tests covering JSON injection, duplicate IDs,
operations on revoked/non-existent keys, PRAGMA convention compliance, and
shared DB lifecycle.

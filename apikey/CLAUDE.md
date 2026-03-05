> **Schema technique** : voir [`apikey_schem.md`](apikey_schem.md) — lecture prioritaire avant tout code source.

# apikey

Responsabilite: Cycle de vie des cles API (horoskeys) — generation, resolution SHA-256, revocation, expiration, scoping services, scoping dossier, rate limit.

## Fiche technique

| Champ | Valeur |
|-------|--------|
| Module | `github.com/hazyhaar/pkg/apikey` |
| Point d'entree | `apikey.go` (fichier unique, ~360 lignes) |
| Depend de | `pkg/trace` (driver `sqlite-trace`) |
| Dependants | `siftrag` (middleware X-API-Key, handlers CRUD, VerifyAccess), `sas_ingester` (KeyResolver callback) |
| DB | SQLite, table `api_keys`, 11 colonnes, 4 index |
| Types exportes | `Store`, `Key`, `Option` |
| Entropie cle | 256 bits (crypto/rand 32 octets) |

## Format de cle

```
hk_7f3a9b2c1d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a
├──┤├─────────────────────── 64 hex chars ───────────────────────────────┤
 3     = 32 random bytes                                    Total: 67 chars
```

- **Prefix stocke** : 8 premiers chars (`hk_7f3a9`) — identification visuelle, pas de securite
- **Hash stocke** : SHA-256 hex de la cle complete — jamais expose via JSON (`json:"-"`)
- **Cle en clair** : retournee UNE FOIS par `Generate()`, jamais persistee

## API

| Methode | Role | Garde |
|---------|------|-------|
| `OpenStore(path)` | Ouvre/cree la DB SQLite, run migrations | — |
| `OpenStoreWithDB(db)` | Wraps un `*sql.DB` existant (DB partagee) | Ne pas appeler `Close()` dessus |
| `Generate(id, ownerID, name, svcs, rateLimit, opts...)` | Genere une cle → (clearKey, *Key, error) | id et ownerID requis |
| `Resolve(clearKey)` | Valide → *Key | Verifie revocation PUIS expiration |
| `Revoke(keyID)` | Revocation irreversible | Double revoke = erreur |
| `List(ownerID)` | Liste cles d'un owner (sans hash) | — |
| `ListByDossier(dossierID)` | Cles actives d'un dossier | Exclut revoquees |
| `SetExpiry(keyID, expiresAt)` | Modifie expiration | Refuse si inexistant/revoque |
| `UpdateServices(keyID, svcs)` | Modifie services autorises | Refuse si inexistant/revoque |
| `HasService(svc)` | Check autorisation service | `nil` services = wildcard (tous) |
| `IsDossierScoped()` | `DossierID != ""` | — |
| `WithDossier(dossierID)` | Option pour `Generate` | — |

## Integration dans l'ecosysteme

```
siftrag                                    sas_ingester
   │                                            │
   │ X-API-Key header                           │ X-Horoskey header
   ▼                                            ▼
middleware.APIKeyAuth()                    WithKeyResolver(func)
   │                                            │
   │ store.Resolve(clearKey)                    │ store.Resolve(clearKey)
   │ authSvc.GetUserByID(key.OwnerID)           │ key.HasService("sas_ingester")
   │ ctx ← user + key                           │ return key.OwnerID
   ▼                                            ▼
VerifyAccess(dossierID, userID, key)       ownerID used as identity
   │
   │ VerifyOwnership + IsDossierScoped check
   ▼
handler logic
```

## Schema SQL

```sql
CREATE TABLE api_keys (
    id          TEXT PRIMARY KEY,
    prefix      TEXT NOT NULL,
    hash        TEXT NOT NULL UNIQUE,
    owner_id    TEXT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    services    TEXT NOT NULL DEFAULT '[]',   -- JSON array via encoding/json
    rate_limit  INTEGER NOT NULL DEFAULT 0,
    dossier_id  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL DEFAULT '',
    revoked_at  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_api_keys_hash    ON api_keys(hash);
CREATE INDEX idx_api_keys_owner   ON api_keys(owner_id);
CREATE INDEX idx_api_keys_prefix  ON api_keys(prefix);
CREATE INDEX idx_api_keys_dossier ON api_keys(dossier_id);
```

## Invariants

- La cle en clair n'est JAMAIS stockee — seul le SHA-256 est persiste
- `Resolve` verifie revocation puis expiration — ordre important
- `Services` vide = wildcard (acces a tous les services)
- `DossierID = ""` = cle legacy (tous les dossiers), `!= ""` = scopee
- Double revocation = erreur (pas idempotent)
- `SetExpiry` et `UpdateServices` refusent sur cle revoquee ou inexistante
- Serialisation services via `encoding/json` (pas de concatenation string)
- Pragmas via DSN `_pragma=` uniquement, jamais `db.Exec("PRAGMA ...")`
- `crypto/rand` uniquement, jamais `math/rand`

## Failles architecturales connues (audit 2026-03-01)

### CRITIQUE — IDOR sur revocation (siftrag)

`HandleRevokeAPIKey` prend `keyID` de l'URL et revoque sans verifier que l'utilisateur authentifie possede la cle. N'importe quel utilisateur connecte peut revoquer les cles d'un autre.

**Localisation** : `siftrag/internal/handlers/apikey.go:80-94`

### HIGH — Rate limit declare mais jamais applique

`rate_limit` est stocke en DB mais aucun middleware ne l'utilise apres `Resolve()`. Le champ existe dans le schema mais c'est une feature morte. Soit l'implementer (via `pkg/ratelimit`), soit le retirer pour ne pas tromper.

### HIGH — Cle en clair dans le flash cookie

`HandleCreateAPIKey` et `HandleRegenerateAPIKey` transmettent la cle en clair via `shield.SetFlash()` (cookie). Les cookies sont logges par des proxies/CDN, ont des limites de taille, et la cle est perdue si l'utilisateur ne voit pas le flash.

### HIGH — Suppression dossier n'orpheline pas les cles

`DossierService.Delete` supprime le dossier et le shard catalog mais ne revoque pas les cles API scopees au dossier. Ces cles deviennent orphelines : valides en DB, pointant vers un dossier supprime.

### MEDIUM — MultiKeyAuth sans cross-check dossier

`MultiKeyAuth` verifie que toutes les cles appartiennent au meme owner mais ne verifie pas la compatibilite des scopes dossier. Une recherche federee avec des cles scopees a des dossiers differents pourrait contourner l'isolation.

### MEDIUM — Pas d'audit trail

Aucune integration avec `pkg/audit`. Creation, revocation et usage des cles ne sont pas traces. Pour un composant de securite, c'est un angle mort.

### LOW — Pas de limite de cles par utilisateur

Aucun plafond sur le nombre de cles. Un utilisateur malveillant peut creer des millions de cles.

### LOW — Close() sur Store partage

`OpenStoreWithDB` wraps un `*sql.DB` externe. `Close()` ferme la DB partagee. Pas de distinction owned/borrowed.

## NE PAS

- Ne pas stocker ou logger la cle en clair
- Ne pas utiliser `db.Exec("PRAGMA ...")` — DSN `_pragma=` uniquement
- Ne pas supposer que `Services = nil` interdit l'acces — c'est un wildcard
- Ne pas appeler `store.Close()` si cree via `OpenStoreWithDB`
- Ne pas revoquer sans verifier l'ownership cote handler

## Build / Test

```bash
CGO_ENABLED=0 go test -v -count=1 ./apikey/...
```

28 tests dont 12 tests d'audit couvrant : injection JSON services, ID duplique, operations sur cle inexistante/revoquee, pragma exec, OpenStoreWithDB.

# apikey

Responsabilite: Cycle de vie des cles API (horoskeys) — generation, resolution (SHA-256), revocation, expiration, scoping services, rate limit.
Depend de: `github.com/hazyhaar/pkg/trace` (driver sqlite-trace)
Dependants: `sas_ingester` (via KeyResolver callback), `siftrag` (via OpenStoreWithDB, X-API-Key middleware), binaires (cmd/*)
Point d'entree: apikey.go
Types cles: `Store`, `Key`

## Format de cle

`hk_` + 32 octets aleatoires hex = 67 caracteres total.
- Prefix stocke : 8 premiers caracteres (`hk_7f3a9`) pour identification sans exposition
- Hash SHA-256 stocke en DB — la cle en clair n'est jamais persistee
- La cle en clair est retournee une seule fois par `Generate()`

## API

| Methode | Role |
|---------|------|
| `OpenStore(path)` | Ouvre/cree la DB SQLite, run migrations |
| `OpenStoreWithDB(db)` | Wraps un `*sql.DB` existant (DB partagee) |
| `Generate(id, ownerID, name, services, rateLimit)` | Genere une cle, retourne (clearKey, *Key, error) |
| `Resolve(clearKey)` | Valide et retourne le `*Key` — verifie revocation + expiration |
| `Revoke(keyID)` | Revoque une cle (irreversible) |
| `List(ownerID)` | Liste les cles d'un owner (sans hash) |
| `SetExpiry(keyID, expiresAt)` | Definit/modifie l'expiration |
| `UpdateServices(keyID, services)` | Modifie les services autorises (pas de rotation de cle) |
| `HasService(service)` | Verifie si une cle a acces a un service (nil = tous) |

## Integration avec sas_ingester

```go
keyStore, _ := apikey.OpenStore("keys.db")
resolver := func(ctx context.Context, horoskey string) (string, error) {
    key, err := keyStore.Resolve(horoskey)
    if err != nil { return "", err }
    if !key.HasService("sas_ingester") { return "", fmt.Errorf("unauthorized") }
    return key.OwnerID, nil
}
ing, _ := sas_ingester.NewIngester(cfg, sas_ingester.WithKeyResolver(resolver))
```

## Schema

```sql
CREATE TABLE api_keys (
    id TEXT PRIMARY KEY, prefix TEXT NOT NULL, hash TEXT NOT NULL UNIQUE,
    owner_id TEXT NOT NULL, name TEXT NOT NULL DEFAULT '',
    services TEXT NOT NULL DEFAULT '[]', rate_limit INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL, expires_at TEXT NOT NULL DEFAULT '',
    revoked_at TEXT NOT NULL DEFAULT ''
);
```

## Invariants

- La cle en clair n'est JAMAIS stockee — seul le SHA-256 est persiste
- `Resolve` verifie revocation puis expiration — ordre important
- `Services` vide (`[]` ou nil) = acces a tous les services
- Double revocation = erreur (idempotence evitee volontairement)
- `HasService` sur cle sans services = true (wildcard)

NE PAS:
- Ne pas stocker ou logger la cle en clair — seul le SHA-256 est persiste
- Ne pas ignorer l'erreur de `Resolve()` — doit verifier revocation avant expiration
- Ne pas supposer que `Services = nil` interdit l'acces — c'est un wildcard (tous les services)
- Ne pas confondre `Prefix` (8 chars, visible, pour identification) avec `Hash` (SHA-256, jamais expose)
- Ne pas utiliser `math/rand` pour la generation — `crypto/rand` obligatoire

## Build / Test

```bash
CGO_ENABLED=0 go test -v -count=1 ./apikey/...
```

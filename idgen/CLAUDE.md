> **Schema technique** : voir [`idgen_schem.md`](idgen_schem.md) — lecture prioritaire avant tout code source.

# idgen

Responsabilite: Generation d'identifiants uniques pluggables (UUIDv7, NanoID, prefixed, timestamped) pour tout l'ecosysteme HOROS.
Depend de: `github.com/google/uuid`
Dependants: `mcpquic/server`, `mcprt/registry`, `mcprt/handlers`, `mcprt/types`, `observability/audit`, `observability/logger`, `sas_ingester/ingester`, `audit/logger`, `feedback/feedback`; externes: repvow (aucun direct), horum (aucun direct), horostracker/internal/db, chrc/domkeeper, chrc/domregistry, chrc/domwatch, chrc/veille
Point d'entree: idgen.go
Types cles: `Generator` (func() string), `Default` (var, UUIDv7)
Invariants:
- `Default` est toujours UUIDv7 (RFC 9562) — ne jamais le remplacer par NanoID globalement
- `NanoID` utilise `crypto/rand` — jamais `math/rand`
- Les constructeurs across pkg/ acceptent un `Generator` — la strategie est un choix au demarrage, pas a la compilation
NE PAS:
- Ne pas utiliser `idgen.New()` dans du code qui sera teste unitairement — injecter un `Generator` deterministe a la place
- Ne pas confondre `Parse`/`MustParse` (validation UUID seule) avec la generation
- Ne pas utiliser NanoID pour des identifiants qui doivent etre triables chronologiquement — utiliser UUIDv7

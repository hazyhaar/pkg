# cmd

Responsabilite: Binaires CLI utilitaires construits au-dessus des packages de la bibliotheque hazyhaar/pkg.
Depend de: `github.com/hazyhaar/pkg/sas_chunker`, `github.com/hazyhaar/pkg/sas_ingester`, `github.com/hazyhaar/pkg/idgen`, `github.com/hazyhaar/pkg/kit`, `github.com/hazyhaar/pkg/observability`, `github.com/hazyhaar/pkg/trace`
Dependants: aucun (ce sont des entry points)
Point d'entree: `sas_chunker/main.go` (split/assemble/verify), `sas_ingester/main.go` (HTTP server ingestion)
Types cles: (pas de types exportes -- ce sont des `package main`)
Invariants:
- `sas_chunker` : CLI pure (split, assemble, verify) -- pas de serveur, pas de DB
- `sas_ingester` : serveur HTTP avec tus resumable upload, JWT auth, observability, trace
- `sas_ingester` utilise 3 DBs separees (app, traces, observability) pour eviter la contention en ecriture
- Le heartbeat ecrit toutes les 15s, le retry loop tourne toutes les 30s
- Build avec `CGO_ENABLED=0`
NE PAS:
- Deployer `sas_ingester` sans configurer `sas_ingester.yaml` (JWTSecret, ChunksDir, DBPath obligatoires)
- Ouvrir la trace DB avec le driver "sqlite-trace" (recursion infinie) -- utiliser le driver "sqlite" brut

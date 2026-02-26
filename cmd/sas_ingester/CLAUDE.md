# sas_ingester (CLI)

Responsabilite: Serveur HTTP d'ingestion de fichiers avec tus resumable uploads, authentification JWT, observabilite et tracing.
Depend de: `github.com/hazyhaar/pkg/sas_ingester`, `github.com/hazyhaar/pkg/idgen`, `github.com/hazyhaar/pkg/kit`, `github.com/hazyhaar/pkg/observability`, `github.com/hazyhaar/pkg/trace`
Dependants: aucun (entry point terminal)
Point d'entree: main.go
Types cles: aucun type exporte (`package main`)
Routes:
- `POST /v1/ingest` — upload multipart avec JWT auth
- `POST /v1/ingest/tus` — creation resumable upload (tus 1.0.0)
- `HEAD|PATCH /v1/ingest/tus/{id}` — resume/complete upload
- `GET|DELETE /v1/dossiers/{id}` — consultation/suppression dossier
- `GET /v1/health` — health check avec heartbeat et compteurs pieces
Invariants:
- 3 bases SQLite separees (app, traces, observability) pour eviter la contention en ecriture
- La trace DB utilise le driver `"sqlite"` brut (pas `"sqlite-trace"` — evite la recursion)
- Heartbeat ecrit toutes les 15s, retry loop toutes les 30s
- `dossier_id` : query param > JWT claim > genere par le serveur (jamais derive de `claims.Sub`)
- Context enrichi par `contextMiddleware` : request ID, trace ID, transport
- Config via `sas_ingester.yaml` (JWTSecret, ChunksDir, DBPath obligatoires)
NE PAS:
- Ouvrir la trace DB avec le driver `"sqlite-trace"` (recursion infinie)
- Deployer sans `sas_ingester.yaml` configure (crash au demarrage)
- Deriver le dossier_id du JWT sub (preserve l'opacite d'identite)

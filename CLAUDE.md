> **Protocole** — Avant toute tâche, lire [`../CLAUDE.md`](../CLAUDE.md) §Protocole de recherche.
> Commandes obligatoires : `Read <dossier>/CLAUDE.md` → `Grep "CLAUDE:SUMMARY"` → `Grep "CLAUDE:WARN" <fichier>`.
> **Interdit** : Bash(grep/cat/find) au lieu de Grep/Read. Ne jamais lire un fichier entier en première intention.
> **Decouverte** — Chaque sous-package : `*_schem.md` EN PREMIER. Schema global : [`../HOROS_ECOSYSTEM_schem.md`](../HOROS_ECOSYSTEM_schem.md).

# hazyhaar/pkg

Responsabilite: Package Go partage de l'ecosysteme HOROS — bibliotheque de composants reutilisables importee par tous les services.
Module: `github.com/hazyhaar/pkg` (prive)

## Sous-packages

| Package | Role |
|---------|------|
| `apikey` | Cycle de vie cles API (generation, SHA-256, revocation, scoping) |
| `auth` | JWT claims, cookie, OAuth Google, middleware auth |
| `authproxy` | Proxy auth FO-BO (login, register, forgot/reset-password) |
| `audit` | Logger d'audit SQLite |
| `chassis` | Unified chassis pattern (HTTP + MCP server) |
| `channels` | Dispatching multi-canal (Discord, Telegram, WhatsApp, webhooks) |
| `chunk` | Decoupage texte RAG avec overlap, paragraph-aware |
| `connectivity` | Router inter-services, circuit breaker, retry, factory HTTP/MCP |
| `docpipe` | Extraction documents multi-format (PDF, DOCX, ODT, HTML, TXT, MD) — pure Go |
| `dbopen` | Helper ouverture SQLite avec pragmas + retry |
| `dbsync` | Replication SQLite BO-FO via QUIC (publisher, subscriber, snapshot) |
| `feedback` | Widget feedback integrable (HTML/CSS/JS + handlers) |
| `horosafe` | Sanitization et validation input |
| `horosembed` | Client embeddings transport-agnostique, vector ops, EmbedFactory |
| `idgen` | Generation UUID v7 |
| `injection` | Detection injection prompt multi-couche (normalize + intent match, zero regex) |
| `kit` | Context helpers, endpoint pattern, MCP tool registration |
| `mcpquic` | Transport QUIC pour MCP (client + server) |
| `mcprt` | Runtime MCP dynamique (bridge, registry, hot-reload tools) |
| `observability` | Audit, heartbeat, metrics, schema |
| `sas_ingester` | Ingestion SAS avec TUS upload, metadata, identity, connectivity, MCP, markdown |
| `shardlog` | JSON-lines shard activity log — zero SQLite, filterable par dossier_id |
| `shield` | Middleware HTTP securite (rate limit, headers, flash, trace ID, CSRF) |
| `trace` | Tracing SQL (store, driver wrapper) |
| `vtq` | Virtual Task Queue pattern |
| `watch` | File/DB watcher (PRAGMA data_version polling) |

## Build / Test

```bash
CGO_ENABLED=0 go build ./...                          # build (library — pas de binaire, sauf sas_ingester)
go test -race -v -count=1 -timeout 120s ./...          # -race obligatoire
make test                                               # equivalent via Makefile (build sas_ingester + tests)
```

CI: GitHub Actions sur `hazyhaar/pkg` — lint + test + gate `CI OK`. Voir root CLAUDE.md section CI/CD.

## Pieges connus

- MCP SDK `go-sdk` v1.3.1 : `ToolHandler` recoit `*mcp.CallToolRequest` (pointeur) ; `error` = JSON-RPC erreur protocole, `result.SetError()` = erreur outil

- `dbsync.AuthProxy` est **deprecated** — utiliser `authproxy.NewAuthProxy`
- `authproxy.ForgotPasswordHandler` envoie `origin` (deduit de `r.Host`/`X-Forwarded-*`) — le BO doit le valider
- `watch` utilise `PRAGMA data_version` (poll 200ms) — pas de filesystem watcher
- Les tests `dbsync_test.go` necessitent du temps (QUIC handshake) — `go test -timeout 60s`
- goleak actif sur vtq, watch, dbsync, mcpquic, chassis, connectivity — fuite goroutine = test fail

## NE PAS

- `db.Exec("PRAGMA ...")` — utiliser `_pragma=` dans le DSN (dbopen)
- `mattn/go-sqlite3` — utiliser `modernc.org/sqlite` (CGO_ENABLED=0)
- Modifier les fichiers `*_templ.go` — generés par `templ generate`

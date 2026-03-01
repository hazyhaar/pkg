# CLAUDE.md — hazyhaar/pkg

> **Découverte** — Chaque sous-package dispose d'un schéma technique ASCII (`{pkg}_schem.md`) dans son dossier. **Lire le `*_schem.md` EN PREMIER** avant tout fichier source. Le schéma global écosystème est à la racine : [`../HOROS_ECOSYSTEM_schem.md`](../HOROS_ECOSYSTEM_schem.md).

> **Règle n°1** — Un bug trouvé en audit mais pas par un test est d'abord une faille de test. Écrire le test rouge, puis fixer. Pas de fix sans test.

## Ce que c'est

Package Go partagé de l'écosystème HOROS. Bibliothèque de composants réutilisables importée par tous les services (repvow, horum, horostracker, touchstone-registry).

**Module** : `github.com/hazyhaar/pkg`
**Repo** : `github.com/hazyhaar/pkg` (privé)

## Sous-packages

| Package | Rôle |
|---------|------|
| `apikey` | Cycle de vie clés API (horoskeys) — génération, résolution SHA-256, révocation, scoping services |
| `auth` | JWT claims, cookie management, OAuth (Google), middleware auth |
| `authproxy` | Proxy auth FO→BO (login, register, forgot-password avec origin, reset-password) |
| `audit` | Logger d'audit SQLite (qui a fait quoi, quand) |
| `chassis` | Unified chassis pattern (HTTP + MCP server) |
| `channels` | Dispatching multi-canal (Discord, Telegram, WhatsApp, webhooks) |
| `chunk` | Découpage texte RAG avec overlap, paragraph-aware (migré depuis chrc) |
| `connectivity` | Router inter-services, circuit breaker, retry, factory HTTP/MCP |
| `docpipe` | Extraction documents multi-format (PDF, DOCX, ODT, HTML, TXT, MD) — pure Go, quality scoring (migré depuis chrc) |
| `dbopen` | Helper ouverture SQLite avec pragmas + retry |
| `dbsync` | Réplication SQLite BO→FO via QUIC (publisher, subscriber, filter, snapshot) |
| `feedback` | Widget feedback intégrable (HTML/CSS/JS + handlers) |
| `horosafe` | Sanitization et validation input |
| `horosembed` | Client embeddings transport-agnostique, vector ops, EmbedFactory (migré depuis chrc) |
| `idgen` | Génération UUID v7 |
| `injection` | Détection injection prompt multi-couche — normalize + intent match exact/fuzzy/base64, zero regex |
| `kit` | Context helpers, endpoint pattern, MCP tool registration |
| `mcpquic` | Transport QUIC pour MCP (client + server) |
| `mcprt` | Runtime MCP dynamique (bridge, registry, hot-reload tools) |
| `observability` | Audit, heartbeat, metrics, schema |
| `sas_chunker` | Chunking de fichiers pour ingestion |
| `sas_ingester` | Ingestion SAS avec TUS upload, metadata, identity, connectivity, MCP, markdown |
| `shield` | Middleware HTTP sécurité (rate limit, headers, flash, trace ID, CSRF) |
| `trace` | Tracing SQL (store, driver wrapper) |
| `vtq` | Virtual Task Queue pattern |
| `watch` | File/DB watcher (PRAGMA data_version polling) |

## Migration MCP SDK (février 2026)

Le projet a migré de `mark3labs/mcp-go` vers `modelcontextprotocol/go-sdk` v1.3.1. Points critiques :

- `ToolHandler` reçoit `*mcp.CallToolRequest` (pointeur) et arguments en `json.RawMessage` (pas `map[string]any`)
- Retourner `error` = erreur JSON-RPC protocole. Pour erreur outil, utiliser `result.SetError(err)` et retourner `(result, nil)`
- `InputSchema` doit avoir `"type": "object"` sinon le SDK refuse l'enregistrement

Voir `CLAUDE.md` interne (section "Migration MCP SDK") pour le tableau de mapping complet.

## Build / Test

```bash
CGO_ENABLED=0 go build ./...
go test -race -v -count=1 -timeout 120s ./...   # -race obligatoire
make test                                         # équivalent via Makefile
```

## CI/CD

**GitHub Actions** actif sur `hazyhaar/pkg`. Chaque push sur `main` et chaque PR déclenche :

1. **Lint** — `golangci-lint run` (installé via `go install`, pas l'action pré-buildée — compat Go 1.25)
2. **Test** — `go test -race -v -count=1 -timeout 120s ./...`
3. **Gate `CI OK`** — bloque le merge si lint ou test échoue

**Workflow** : `.github/workflows/ci.yml`
**Branch protection** : `main` protégée — status check `CI OK` requis, force push interdit.

### Règles pour les agents Claude

- **Toujours lancer `make test` localement** avant de proposer un commit
- **Le flag `-race` est obligatoire** dans les tests — ne jamais le retirer
- **Ne pas ignorer les erreurs de lint** — les corriger ou les exclure explicitement dans `.golangci.yml`
- **goleak** est actif sur vtq, watch, dbsync, mcpquic, chassis, connectivity — tout test qui fuite une goroutine échouera
- **Si un test échoue en CI mais pas en local** : c'est un bug (timing, env-dependent). Le fixer, pas le skip

## Pièges connus

- `dbsync.AuthProxy` est **deprecated** — utiliser `authproxy.NewAuthProxy` à la place
- `authproxy.ForgotPasswordHandler` envoie `origin` (déduit de `r.Host`/`X-Forwarded-*`) — le BO doit le valider
- `watch` utilise `PRAGMA data_version` (poll 200ms) — pas de filesystem watcher
- Les tests `dbsync_test.go` nécessitent du temps (QUIC handshake) — `go test -timeout 60s`

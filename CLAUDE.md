# CLAUDE.md — hazyhaar/pkg

## Ce que c'est

Package Go partagé de l'écosystème HOROS. Bibliothèque de composants réutilisables importée par tous les services (repvow, horum, horostracker, touchstone-registry).

**Module** : `github.com/hazyhaar/pkg`
**Repo** : `github.com/hazyhaar/pkg` (privé)

## Sous-packages

| Package | Rôle |
|---------|------|
| `auth` | JWT claims, cookie management, OAuth (Google), middleware auth |
| `authproxy` | Proxy auth FO→BO (login, register, forgot-password avec origin, reset-password) |
| `audit` | Logger d'audit SQLite (qui a fait quoi, quand) |
| `chassis` | Unified chassis pattern (HTTP + MCP server) |
| `channels` | Dispatching multi-canal (Discord, Telegram, WhatsApp, webhooks) |
| `connectivity` | Router inter-services, circuit breaker, retry, factory HTTP/MCP |
| `dbopen` | Helper ouverture SQLite avec pragmas + retry |
| `dbsync` | Réplication SQLite BO→FO via QUIC (publisher, subscriber, filter, snapshot) |
| `feedback` | Widget feedback intégrable (HTML/CSS/JS + handlers) |
| `horosafe` | Sanitization et validation input |
| `idgen` | Génération UUID v7 |
| `kit` | Context helpers, endpoint pattern, MCP tool registration |
| `mcpquic` | Transport QUIC pour MCP (client + server) |
| `mcprt` | Runtime MCP dynamique (bridge, registry, hot-reload tools) |
| `observability` | Audit, heartbeat, metrics, schema |
| `sas_chunker` | Chunking de fichiers pour ingestion |
| `sas_ingester` | Ingestion SAS avec TUS upload, metadata, identity |
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
go test ./... -count=1
```

## Pièges connus

- `dbsync.AuthProxy` est **deprecated** — utiliser `authproxy.NewAuthProxy` à la place
- `authproxy.ForgotPasswordHandler` envoie `origin` (déduit de `r.Host`/`X-Forwarded-*`) — le BO doit le valider
- `watch` utilise `PRAGMA data_version` (poll 200ms) — pas de filesystem watcher
- Les tests `dbsync_test.go` nécessitent du temps (QUIC handshake) — `go test -timeout 60s`

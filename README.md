# hazyhaar/pkg

Packages Go partagés de l'écosystème HOROS. Bibliothèques réutilisables pour construire des services distribués communicant via MCP over QUIC.

## Packages

| Package | Description |
|---------|-------------|
| **audit** | Logger d'audit SQLite avec rétention configurable |
| **auth** | JWT HS256 (HorosClaims), middleware, OAuth2 Google, cookies SSO |
| **channels** | Dispatcher de notifications multi-canal (webhook, email) |
| **chassis** | Serveur unifié HTTP/3 + MCP, TLS auto-signé pour dev |
| **connectivity** | Routeur fédération inter-services, circuit breaker, MCPFactory |
| **feedback** | Widget de feedback embeddable (JS/CSS/JSON endpoint) |
| **idgen** | Générateur d'identifiants UUIDv7 préfixés (usr\_, eng\_, pnt\_...) |
| **kit** | Endpoint fonctionnel, context helpers, RegisterMCPTool, Chain |
| **mcpquic** | Transport MCP over QUIC (listener + client) |
| **mcprt** | Registry d'outils MCP dynamiques avec hot-reload SQLite |
| **observability** | Métriques et health checks |
| **trace** | Driver SQLite instrumenté (sqlite-trace), store de traces |
| **watch** | Polling réactif via PRAGMA data_version |

## Principes

- **Pure Go** — `CGO_ENABLED=0`, aucune dépendance C
- **SQLite** — `modernc.org/sqlite` uniquement
- **UUIDv7** — Identifiants triables chronologiquement
- **Library-first** — Chaque package est importable indépendamment

## Installation

```bash
go get github.com/hazyhaar/pkg@latest
```

Importer un package spécifique :

```go
import (
    "github.com/hazyhaar/pkg/auth"
    "github.com/hazyhaar/pkg/kit"
    "github.com/hazyhaar/pkg/audit"
)
```

## Licence

[MIT](LICENSE)

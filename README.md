# hazyhaar/pkg

Packages Go partagés de l'écosystème HOROS. Bibliothèques réutilisables pour construire des services distribués communicant via MCP over QUIC.

## Packages

| Package | Description |
|---------|-------------|
| **audit** | Logger d'audit SQLite asynchrone avec rétention configurable |
| **auth** | JWT HS256 (HorosClaims), middleware, OAuth2 Google, cookies SSO |
| **channels** | Dispatcher de notifications multi-canal (webhook, email, Discord, Telegram, WhatsApp) |
| **chassis** | Serveur unifié HTTP/1.1 + HTTP/2 + HTTP/3 + MCP-over-QUIC sur un seul port |
| **connectivity** | Routeur fédération inter-services, circuit breaker, retry, MCPFactory |
| **dbsync** | Réplication de base de données BO→FO via snapshots QUIC filtrés |
| **feedback** | Widget de feedback embeddable (JS/CSS/JSON endpoint) |
| **idgen** | Générateur d'identifiants UUIDv7 (default) et NanoID, avec préfixes |
| **kit** | Endpoint fonctionnel, context helpers, RegisterMCPTool, Chain |
| **mcpquic** | Transport MCP over QUIC (listener + client) avec framing magic bytes |
| **mcprt** | Registry d'outils MCP dynamiques avec hot-reload SQLite |
| **observability** | Métriques SQLite, audit trail, event logger, heartbeat, health checks |
| **sas_chunker** | Découpage/réassemblage de fichiers volumineux avec vérification SHA-256 |
| **sas_ingester** | Système d'ingestion de fichiers avec tus (upload résumable) et JWT |
| **trace** | Driver SQLite instrumenté (`sqlite-trace`), store de traces asynchrone |
| **watch** | Polling réactif via `PRAGMA data_version` avec debounce configurable |

## Principes

- **Pure Go** — `CGO_ENABLED=0`, aucune dépendance C
- **SQLite** — `modernc.org/sqlite` uniquement
- **UUIDv7** — Identifiants triables chronologiquement (RFC 9562), convention écosystème
- **Library-first** — Chaque package est importable indépendamment
- **Hot-reload** — Pattern `watch` → detect → debounce → reload, zero-downtime
- **Job as Library** — Code monolithique, déploiement microservices via routes SQLite

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

## Documentation par package

### audit

Logger d'audit SQLite avec buffering asynchrone (canal de 256 entrées, batch de 32, flush toutes les 500ms).

```go
logger := audit.NewSQLiteLogger(db, audit.WithIDGenerator(idgen.Prefixed("aud_", idgen.Default)))
logger.Init()
defer logger.Close()

// Middleware kit : capture automatique durée, params, résultat, erreur
endpoint = kit.Chain(audit.Middleware(logger, "create_dossier"))(endpoint)
```

**Types exportés** : `SQLiteLogger`, `Entry`, `Logger` (interface), `Middleware`, `Option`, `WithIDGenerator`.

---

### auth

Authentification JWT HS256 avec claims HOROS, OAuth2 Google, cookies cross-domain.

```go
token, _ := auth.GenerateToken(secret, auth.HorosClaims{
    RegisteredClaims: jwt.RegisteredClaims{Subject: "user123"},
    Email: "user@horos.dev", Role: "admin",
})
claims, _ := auth.ValidateToken(token, secret)

// Middleware HTTP
mux.Handle("/api/", auth.Middleware(secret)(apiHandler))
```

**Types exportés** : `HorosClaims`, `GenerateToken`, `ValidateToken`, `Middleware`, `SetTokenCookie`, `ClearTokenCookie`, `OAuthConfig`, `NewGoogleProvider`, `FetchGoogleUser`.

---

### channels

Dispatcher de messagerie multi-canal avec hot-reload base de données. Supporte webhook (HMAC-SHA256), stubs Discord, Telegram, WhatsApp.

```go
dispatcher := channels.NewDispatcher(db, channels.WithFactory("webhook", channels.WebhookFactory()))
dispatcher.Watch(ctx)  // hot-reload via PRAGMA data_version
dispatcher.Send(ctx, "webhook_1", []byte(`{"text":"hello"}`))
```

**Types exportés** : `Channel` (interface), `Dispatcher`, `Admin`, `ChannelFactory`, `InboundHandler`, `WebhookFactory`.

---

### chassis

Serveur unifié multiplexant HTTP/1.1, HTTP/2, HTTP/3 et MCP-over-QUIC sur un seul port via ALPN.

```go
srv, _ := chassis.New(chassis.Config{
    Addr: ":8443",
    Handler: mux,
    MCPServer: mcpServer,
    TLS: chassis.DevelopmentTLSConfig(),
})
srv.Start(ctx)
```

**Types exportés** : `Server`, `Config`, `DevelopmentTLSConfig`, `ProductionTLSConfig`, `GenerateSelfSignedCert`.

---

### connectivity

Routeur de fédération inter-services avec dispatch local/remote, circuit breaker, retry, timeout, fallback. Routes gérées en SQLite avec hot-reload.

```go
router := connectivity.NewRouter(db,
    connectivity.WithFactory("http", connectivity.HTTPFactory()),
    connectivity.WithFactory("mcp", connectivity.MCPFactory(tlsCfg)),
)
go router.Watch(ctx)

result, _ := router.Call(ctx, "service-name", payload)
```

**Types exportés** : `Router`, `Admin`, `Handler`, `TransportFactory`, `CircuitBreaker`, `Chain`, `WithRetry`, `WithFallback`, `WithCircuitBreaker`, `ErrServiceNotFound`, `ErrCircuitOpen`.

---

### dbsync

Réplication base de données back-office → front-office. Snapshots filtrés (tables, colonnes, WHERE), transport QUIC, vérification SHA-256, swap atomique.

```go
spec := dbsync.FilterSpec{
    FullTables:     []string{"products", "categories"},
    FilteredTables: map[string]string{"orders": "status = 'public'"},
    PartialTables:  map[string]dbsync.PartialTable{
        "users": {Columns: []string{"id", "name"}, Where: "is_active = 1"},
    },
}
pub := dbsync.NewPublisher(srcDB, spec, quicDialer, opts...)
pub.Start(ctx)
```

**Types exportés** : `Publisher`, `Subscriber`, `FilterSpec`, `PartialTable`, `SnapshotMeta`, `ProduceSnapshot`, `ValidateFilterSpec`, `WriteProxy`, `RedirectHandler`.

---

### feedback

Widget de feedback embeddable. Backend Go + frontend JS/CSS injectable dans n'importe quel service HTTP.

```go
widget, _ := feedback.New(feedback.Config{
    DB: db, AppName: "horum",
    UserIDFn: func(r *http.Request) string { return auth.GetUserID(r) },
})
widget.RegisterMux(mux, "/feedback")
```

**Endpoints** : `POST /submit`, `GET /comments`, `GET /comments.html`, `GET /widget.js`, `GET /widget.css`.

---

### idgen

Génération d'identifiants pluggable. **Default : UUIDv7** (RFC 9562, triable chronologiquement). NanoID disponible pour les cas où la brièveté prime.

```go
id := idgen.New()                                    // UUIDv7
id = idgen.Prefixed("dos_", idgen.Default)()          // "dos_019513ab-..."
id = idgen.NanoID(8)()                                // "a3f8k2p1" (court)
id = idgen.Timestamped(idgen.NanoID(6))()             // "20260221T120000Z_abc123"

parsed, err := idgen.Parse("01234567-89ab-7cde-8f01-234567890abc")
```

**Types exportés** : `Generator`, `Default`, `New`, `UUIDv7`, `NanoID`, `Prefixed`, `Timestamped`, `Parse`, `MustParse`.

---

### kit

Primitives transport-agnostiques : endpoint fonctionnel, middleware, context helpers, bridge MCP.

```go
type Endpoint func(ctx context.Context, request any) (response any, err error)
type Middleware func(Endpoint) Endpoint

endpoint = kit.Chain(audit.Middleware(logger, "action"), auth.RequireAuth())(baseEndpoint)

ctx = kit.WithUserID(ctx, "user123")
ctx = kit.WithRequestID(ctx, idgen.New())
userID := kit.GetUserID(ctx)
```

**Types exportés** : `Endpoint`, `Middleware`, `Chain`, `WithUserID`, `GetUserID`, `WithRequestID`, `GetRequestID`, `WithTraceID`, `GetTraceID`, `WithTransport`, `GetTransport`, `RegisterMCPTool`.

---

### mcpquic

Transport MCP (Model Context Protocol) sur QUIC. Client et serveur avec ALPN, magic bytes framing, TLS 1.3.

```go
// Serveur (via chassis)
handler := mcpquic.NewHandler(mcpServer, mcpquic.WithHandlerIDGenerator(idgen.NanoID(8)))

// Client
client, _ := mcpquic.NewClient(ctx, "localhost:8443", mcpquic.ClientTLSConfig())
tools, _ := client.ListTools(ctx)
result, _ := client.CallTool(ctx, "search", map[string]any{"q": "hello"})
```

**Types exportés** : `Client`, `Handler`, `Listener`, `ServerTLSConfig`, `ClientTLSConfig`, `SelfSignedTLSConfig`, `ALPNProtocolMCP`.

---

### mcprt

Registry dynamique d'outils MCP chargés depuis SQLite. Hot-reload via watch. Handlers SQL (query/script) et Go.

```go
reg := mcprt.NewRegistry(db, mcprt.WithIDGenerator(idgen.Default))
reg.RegisterGoFunc("compute_stats", computeStatsFunc)
go reg.Watch(ctx)

result, _ := reg.ExecuteTool(ctx, "compute_stats", params)
```

**Types exportés** : `Registry`, `DynamicTool`, `SQLQueryHandler`, `SQLScriptHandler`, `RegisterGoFunc`.

---

### observability

Observabilité SQLite-native : audit logger, metrics manager, event logger, heartbeat writer, health endpoint.

```go
observability.Init(obsDB)
audit := observability.NewAuditLogger(obsDB, 1000,
    observability.WithAuditIDGenerator(idgen.Prefixed("aud_", idgen.Default)),
)
metrics := observability.NewMetricsManager(obsDB, 100, 5*time.Second)
heartbeat := observability.NewHeartbeatWriter(obsDB, "sas_ingester", 15*time.Second)
heartbeat.Start(ctx)
```

**Types exportés** : `AuditLogger`, `MetricsManager`, `EventLogger`, `HeartbeatWriter`, `Metric`, `Init`, `LatestHeartbeat`.

---

### sas_chunker

Découpage de fichiers volumineux en chunks avec manifeste JSON et vérification SHA-256. Streaming support via `SplitReader`.

```go
manifest, _ := sas_chunker.Split("large_file.bin", "/tmp/chunks", 10*1024*1024, nil)
err := sas_chunker.Assemble("/tmp/chunks", "reassembled.bin", progressFn)
result, _ := sas_chunker.Verify("/tmp/chunks")
```

**Types exportés** : `Split`, `SplitReader`, `Assemble`, `Verify`, `LoadManifest`, `Manifest`, `ChunkMeta`, `VerifyResult`, `FormatBytes`.

---

### sas_ingester

Système d'ingestion de fichiers SAS avec upload résumable (protocole tus), authentification JWT, gestion de dossiers, et pipeline de routage.

```go
ing, _ := sas_ingester.NewIngester(cfg,
    sas_ingester.WithIDGenerator(idgen.Prefixed("dos_", idgen.Default)),
    sas_ingester.WithAudit(auditLogger),
)
result, _ := ing.IngestWithToken(file, dossierID, ownerSub, token)
```

**Types exportés** : `Ingester`, `TusHandler`, `Store`, `Router`, `Config`, `LoadConfig`, `JWTClaims`, `ParseJWT`.

---

### trace

Driver SQLite instrumenté qui intercepte toutes les requêtes SQL. Enregistre durée, requête, erreur. Logging adaptatif (Debug, Warn >100ms, Error).

```go
import _ "github.com/hazyhaar/pkg/trace"

traceDB, _ := sql.Open("sqlite", "traces.db")
store := trace.NewStore(traceDB)
store.Init()
trace.SetStore(store)

db, _ := sql.Open("sqlite-trace", "app.db") // toutes les requêtes tracées
```

**Types exportés** : `Store`, `Entry`, `NewStore`, `SetStore`, `Schema`.

---

### watch

Boucle réactive poll → detect → debounce → action pour SQLite. Détecteurs intégrés : `PRAGMA data_version`, `PRAGMA user_version`, `MAX(column)`.

```go
w := watch.New(db, watch.Options{
    Interval: 200 * time.Millisecond,
    Debounce: 500 * time.Millisecond,
})
go w.OnChange(ctx, func() error { return service.Reload() })
w.WaitForVersion(ctx, targetVersion)
```

**Types exportés** : `Watcher`, `Options`, `Stats`, `ChangeDetector`, `PragmaDataVersion`, `PragmaUserVersion`, `MaxColumnDetector`.

## Dépendances

| Dépendance | Version | Usage |
|------------|---------|-------|
| `github.com/golang-jwt/jwt/v5` | v5.3.1 | JWT HS256 (auth) |
| `github.com/google/uuid` | v1.6.0 | UUIDv7 (idgen) |
| `github.com/mark3labs/mcp-go` | v0.44.0 | Model Context Protocol |
| `github.com/quic-go/quic-go` | v0.59.0 | Transport QUIC/HTTP3 |
| `golang.org/x/oauth2` | v0.35.0 | OAuth2 Google (auth) |
| `gopkg.in/yaml.v3` | v3.0.1 | Configuration YAML |
| `modernc.org/sqlite` | v1.45.0 | SQLite pure Go (CGO_ENABLED=0) |

## Convention ID

**UUIDv7** est la convention écosystème (RFC 9562). Tous les IDs sont générés via `idgen.Default` (= `idgen.UUIDv7()`). Les préfixes de type sont ajoutés via `idgen.Prefixed()` :

| Préfixe | Domaine | Exemple |
|---------|---------|---------|
| `aud_` | Entrées d'audit | `aud_019513ab-...` |
| `evt_` | Événements métier | `evt_019513ab-...` |
| `dos_` | Dossiers (SAS) | `dos_019513ab-...` |
| `req_` | Requêtes HTTP | `req_019513ab-...` |
| `tus_` | Uploads résumables | `tus_019513ab-...` |

**Exception justifiée** : `mcpquic` utilise `NanoID(8)` pour les session IDs (éphémères, brièveté requise).

## Licence

[MIT](LICENSE)

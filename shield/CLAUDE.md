# shield

Responsabilite: Middleware HTTP securite reutilisable — rate limiting SQLite, security headers (CSP/X-Frame/nosniff), flash messages cookie, trace ID, max body, HEAD-to-GET, et maintenance mode.
Depend de: `github.com/hazyhaar/pkg/kit`
Dependants: aucun dans pkg/; externes: repvow/internal/middleware (tous), repvow/internal/handlers, repvow/cmd/repvow, horum/internal/middleware (tous)
Point d'entree: shield.go (DefaultFOStack, DefaultBOStack, context keys)
Types cles: `RateLimiter`, `RateLimitConfig`, `MaintenanceMode`, `HeaderConfig`, `FlashMessage`
Invariants:
- `DefaultFOStack` applique les middlewares dans l'ordre: Maintenance, HeadToGet, SecurityHeaders, MaxFormBody, TraceID, RateLimiter, Flash
- `DefaultBOStack` n'inclut PAS de rate limiter (BO non expose publiquement)
- Le flash cookie a un TTL de 10s, HttpOnly, SameSite=Lax
- Le trace ID est 4 octets random (8 hex chars) — injecte dans le contexte via `kit.WithTraceID`
- Le RateLimiter fait un GC des buckets expires toutes les 5 minutes
- La maintenance flag est dans une table SQLite single-row (`id = 1`) — rechargee toutes les 5s
- `SetDB()` sur RateLimiter et MaintenanceMode permet le swap de connexion apres dbsync
NE PAS:
- Ne pas utiliser `DefaultFOStack` sur un BO — pas de rate limiting necessaire, utiliser `DefaultBOStack`
- Ne pas oublier `StartReloader(done)` sur le MaintenanceMode retourne par `DefaultFOStack`
- Ne pas oublier `shield.Init(db)` pour creer les tables `rate_limits` et `maintenance`
- Ne pas confondre les API paths (/api/*) qui recevant un 429 JSON et les pages qui recevant un redirect avec flash

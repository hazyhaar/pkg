> **Schema technique** : voir [`connectivity_schem.md`](connectivity_schem.md) — lecture prioritaire avant tout code source.

# connectivity

Responsabilite: Smart service router dispatchant les appels localement (in-memory) ou a distance (QUIC/HTTP/MCP) selon une table SQLite rechargee a chaud -- pattern "Job as Library".
Depend de: `github.com/hazyhaar/pkg/dbopen`, `github.com/hazyhaar/pkg/mcpquic`, `github.com/hazyhaar/pkg/horosafe`, `github.com/hazyhaar/pkg/observability`
Dependants: chrc/veille, chrc/vecbridge, chrc/domwatch, chrc/domregistry, HORAG, pkg/horosembed, dbsync/factory
Point d'entree: `router.go` (Router.Call, Router.Reload, Router.Watch)
Types cles: `Router`, `Handler`, `TransportFactory`, `Admin`, `CircuitBreaker`, `HandlerMiddleware`
Invariants:
- Resolution Call : noop -> remote -> local -> erreur (dans cet ordre)
- Seules les routes dont le fingerprint (strategy|endpoint|config) change sont reconstruites au Reload
- `Watch` utilise `PRAGMA data_version` (poll, pas filesystem watcher) -- interval recommande 200ms
- HTTPFactory valide les URLs contre SSRF (via horosafe.ValidateURL) a la creation
- MCPFactory se connecte eagerly (fail fast au Reload, pas au premier Call)
- Strategies supportees dans le schema CHECK : local, quic, http, mcp, dbsync, embed, noop
- "noop" = feature flag silencieux (retourne nil, nil)
NE PAS:
- Appeler `Call` avant `Watch` ou `Reload` -- aucune route ne sera chargee
- Oublier `RegisterTransport` pour chaque strategie utilisee dans la table routes
- Confondre `Close()` du router (ferme les handlers remote) avec `Close()` d'un handler individuel
- Passer des endpoints privees/loopback dans HTTPFactory (SSRF guard les rejettera)

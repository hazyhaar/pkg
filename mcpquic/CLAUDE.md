# mcpquic

Responsabilite: Transport MCP-over-QUIC (client + server + listener) avec ALPN "mcp-quic-v1", magic bytes "MCP1", et TLS 1.3 obligatoire.
Depend de: `github.com/modelcontextprotocol/go-sdk/mcp`, `github.com/quic-go/quic-go`, `github.com/hazyhaar/pkg/idgen`, `github.com/hazyhaar/pkg/kit`
Dependants: `chassis/server`, `chassis/tls`, `connectivity/factory_mcp`; externes: repvow/cmd/repvow, horum/cmd/horum, chrc/cmd/chrc
Point d'entree: server.go (Handler, Listener), client.go (Client)
Types cles: `Client`, `Handler`, `Listener`, `ConnectionError`, `quicServerTransport`, `streamWriteCloser`
Invariants:
- ALPN doit etre "mcp-quic-v1" — toute autre valeur = rejet immediat (ConnErrorUnsupportedALPN)
- Magic bytes "MCP1" envoyes par le client immediatement apres ouverture du stream — defense-in-depth
- TLS 1.3 minimum (`MinVersion: tls.VersionTLS13`) — jamais TLS 1.2
- `0-RTT` desactive (`Allow0RTT: false`) pour eviter les attaques replay
- Le `Store` de trace ne doit jamais utiliser "sqlite-trace" pour sa propre connexion (recursion infinie)
NE PAS:
- Ne pas creer un `Client` sans `tlsCfg` — `ClientTLSConfig(false)` est utilise par defaut (verification certificat active)
- Ne pas oublier `ValidateMagicBytes` cote serveur avant de lancer la session MCP
- Ne pas utiliser le `Listener` standalone si le chassis est actif — le chassis demuxe les connexions QUIC par ALPN
- Ne pas confondre `Handler` (per-connection, utilise par le chassis) et `Listener` (standalone accept loop)

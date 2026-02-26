# chassis

Responsabilite: Serveur unifie exposant HTTP/1.1+HTTP/2 sur TCP et HTTP/3+MCP sur QUIC, meme port, avec ALPN demux.
Depend de: `github.com/hazyhaar/pkg/mcpquic`, `github.com/quic-go/quic-go`, `github.com/quic-go/quic-go/http3`, `github.com/modelcontextprotocol/go-sdk/mcp`
Dependants: horostracker (main), touchstone-registry-audit (server/main)
Point d'entree: `server.go` (Server.Start, Server.Stop)
Types cles: `Server`, `Config`, `GenerateSelfSignedCert`, `DevelopmentTLSConfig`, `ProductionTLSConfig`
Invariants:
- TCP et UDP ecoutent sur le MEME port -- TCP pour HTTP/1.1+HTTP/2, UDP/QUIC pour HTTP/3 et MCP
- Le demux QUIC se fait par ALPN : "h3" -> HTTP/3, "mcp-quic-v1" -> MCP handler
- En dev (pas de CertFile/KeyFile), un certificat ECDSA P-256 self-signed est genere automatiquement
- TLS 1.3 minimum (pas de fallback TLS 1.2)
- Alt-Svc header est injecte automatiquement pour advertiser HTTP/3
- MCPServer peut etre nil -- dans ce cas les connexions QUIC MCP sont rejetees avec erreur applicative
NE PAS:
- Utiliser en production sans fournir CertFile+KeyFile (le self-signed est pour le dev uniquement)
- Oublier d'appeler Stop() -- les listeners TCP et UDP ne se ferment pas automatiquement
- Mixer les NextProtos TCP et QUIC -- le chassis gere ca en interne

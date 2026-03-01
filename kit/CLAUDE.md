> **Schema technique** : voir [`kit_schem.md`](kit_schem.md) — lecture prioritaire avant tout code source.

# kit

Responsabilite: Context helpers, endpoint pattern transport-agnostique, et bridge MCP tool registration pour partager la logique metier entre HTTP et MCP.
Depend de: `github.com/modelcontextprotocol/go-sdk/mcp`
Dependants: `mcpquic/server`, `mcprt/policy`, `shield/trace`, `trace/driver`, `auth/middleware`, `audit/middleware`; externes: repvow/internal/actions, repvow/internal/handlers, repvow/internal/database, horum/internal/actions, horum/internal/handlers, horum/internal/database, horostracker/internal/mcp, chrc/domkeeper/mcp, chrc/domregistry/mcp, chrc/veille/mcp, chrc/docpipe/mcp, chrc/vecbridge/mcp, chrc/horosembed/mcp
Point d'entree: context.go (context keys), endpoint.go (Endpoint/Middleware), transport_mcp.go (RegisterMCPTool)
Types cles: `Endpoint` (func(ctx, any) (any, error)), `Middleware` (func(Endpoint) Endpoint), `MCPDecodeResult`, context keys (`UserIDKey`, `TraceIDKey`, `SessionIDKey`, `RoleKey`, `TransportKey`)
Invariants:
- `GetTransport()` retourne "http" par defaut si aucune valeur n'est dans le contexte
- `RegisterMCPTool` utilise `result.SetError()` pour les erreurs outil, jamais `return error` (qui serait une erreur protocole JSON-RPC)
- Les context keys sont des `contextKey` prives (pas string bruts) pour eviter les collisions
NE PAS:
- Ne pas retourner `error` dans un `ToolHandler` enregistre via `RegisterMCPTool` — c'est une erreur JSON-RPC protocole, pas une erreur outil
- Ne pas acceder aux arguments MCP via `map[string]any` — ils arrivent en `json.RawMessage` dans `req.Params.Arguments` (SDK officiel)
- Ne pas oublier `EnrichCtx` dans `MCPDecodeResult` — c'est le mecanisme pour propager l'identite depuis MCP vers les endpoints

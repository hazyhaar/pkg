# CLAUDE.md — Notes pour agents travaillant sur ce repo

## Migration MCP SDK (février 2026)

Le projet a migré de `mark3labs/mcp-go` (community SDK, v0.44.0) vers
`modelcontextprotocol/go-sdk` (SDK officiel, v1.3.1).

### Changements architecturaux critiques

**Avant (mcp-go)** : boucle manuelle read→HandleMessage→write, gestion explicite
des sessions (RegisterSession/UnregisterSession), notifications via channel.

**Après (official SDK)** : le SDK possède la boucle JSON-RPC. On implémente
`mcp.Transport` → `mcp.Connection`, puis `server.Connect(ctx, transport, nil)`
bloque jusqu'à fin de session. Le SDK gère sessions, init handshake, et dispatch.

### Mapping des types — aide au debug

| Ancien (mcp-go)                        | Nouveau (official SDK)                         |
|----------------------------------------|------------------------------------------------|
| `server.MCPServer`                     | `mcp.Server`                                   |
| `server.NewMCPServer(name, ver)`       | `mcp.NewServer(&mcp.Implementation{...}, nil)` |
| `srv.HandleMessage(ctx, rawJSON)`      | SUPPRIMÉ — géré par le SDK via Transport       |
| `server.RegisterSession / Unregister`  | SUPPRIMÉ — géré par `server.Connect()`         |
| `mcp.NewToolWithRawSchema(n, d, json)` | `&mcp.Tool{Name: n, Description: d, InputSchema: json.RawMessage(raw)}` |
| `srv.AddTool(tool, handler)`           | `srv.AddTool(tool, handler)` — signature handler changée |
| `func(ctx, mcp.CallToolRequest)`       | `func(ctx, *mcp.CallToolRequest)` (pointeur!)  |
| `req.GetArguments() map[string]any`    | `req.Params.Arguments json.RawMessage` (à unmarshal manuellement) |
| `mcp.NewToolResultText(s)`             | `&mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}` |
| `mcp.NewToolResultError(s)`            | `var r mcp.CallToolResult; r.SetError(err)`    |
| `client.NewClient(transport)`          | `mcp.NewClient(&mcp.Implementation{...}, nil)` |
| `client.Start(ctx) + Initialize(ctx)`  | `client.Connect(ctx, transport, nil)` (tout-en-un) |
| `client.ListTools(ctx, req)`           | `session.ListTools(ctx, *ListToolsParams)`     |
| `client.CallTool(ctx, req)`            | `session.CallTool(ctx, *CallToolParams)`       |
| `transport.NewIO(r, w, closer)`        | `&mcp.IOTransport{Reader: r, Writer: w}`       |

### Pièges connus / zones de risque

1. **Arguments en json.RawMessage** : l'ancien SDK décodait automatiquement les
   arguments en `map[string]any`. Le nouveau les laisse en `json.RawMessage`.
   Si un tool handler reçoit des arguments nil/vides et crashe, vérifier le
   parsing dans `mcprt/bridge.go:29` et `kit/transport_mcp.go:20`.

2. **ToolHandler retourne error = erreur protocole** : avec le SDK officiel,
   retourner une `error` non-nil depuis un `ToolHandler` est une erreur
   JSON-RPC (protocole), PAS une erreur outil. Pour les erreurs outil,
   utiliser `result.SetError(err)` et retourner `(result, nil)`.

3. **Session lifecycle** : `ServerSession.Wait()` bloque jusqu'à déconnexion.
   Si le client QUIC se déconnecte brutalement sans fermer le stream, vérifier
   que le timeout QUIC (`DefaultIdleTimeout`) libère bien la goroutine.

4. **InputSchema doit être type "object"** : le SDK officiel valide le schema.
   Si `mcprt/bridge.go` reçoit un DynamicTool dont l'InputSchema n'a pas
   `"type": "object"`, le SDK peut refuser l'enregistrement.

5. **`mcp.Content` est une interface** : `TextContent`, `ImageContent`,
   `EmbeddedResource` l'implémentent. Ne jamais passer un `Content` nil dans
   le slice — le marshaling JSON paniquera.

6. **`Underlying()` supprimé** : `mcpquic.Client.Underlying()` qui retournait
   `*client.Client` (mcp-go) a été supprimé. Non utilisé dans le codebase
   actuel mais si un consommateur externe l'utilisait, il aura une erreur de
   compilation.

### Fichiers impactés

- `mcpquic/server.go` — Transport QUIC serveur, sessionConn wrapper
- `mcpquic/client.go` — Transport QUIC client, session lifecycle
- `kit/transport_mcp.go` — RegisterMCPTool (signature publique changée)
- `mcprt/bridge.go` — Bridge dynamique, raw schema, argument parsing
- `chassis/server.go` — Type MCPServer dans Config
- `connectivity/factory_mcp.go` — AUCUN changement (abstraction mcpquic.Client)

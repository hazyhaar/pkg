> **Schema technique** : voir [`mcprt_schem.md`](mcprt_schem.md) — lecture prioritaire avant tout code source.

# mcprt

Responsabilite: Runtime MCP dynamique — registre d'outils SQLite avec hot-reload, bridge vers mcp.Server, policy RBAC, handlers SQL/Go, et audit hooks.
Depend de: `github.com/modelcontextprotocol/go-sdk/mcp`, `github.com/hazyhaar/pkg/idgen`, `github.com/hazyhaar/pkg/kit`, `github.com/hazyhaar/pkg/watch`
Dependants: aucun dans pkg/ (package terminal); externes: repvow/cmd/repvow, horum/cmd/horum, horostracker/main
Point d'entree: types.go (types fondamentaux), registry.go (Registry), bridge.go (Bridge), handlers.go (SQLQueryHandler, SQLScriptHandler), policy.go (DBPolicy)
Types cles: `DynamicTool`, `Registry`, `GoFunc`, `PolicyFunc`, `AuditFunc`, `ToolHandler` (interface), `SQLQueryHandler`, `SQLScriptHandler`, `DBPolicy`
Invariants:
- `InputSchema` doit avoir `"type":"object"` sinon le SDK refuse l'enregistrement
- Mode `readonly` interdit les `sql_script` handlers et verifie `isReadOnlySQL` pour les `sql_query`
- Le trigger `trg_mcp_tools_updated_at` depend de `recursive_triggers = OFF` (defaut SQLite)
- `maxQueryRows` (10000) est un plafond dur — jamais de result set illimite
- Les expressions template `{{ }}` dans `sql_script` n'acceptent que `uuid()`, `now()`, et les noms de params — jamais de fonctions arbitraires
NE PAS:
- Ne pas oublier `Registry.Init()` au demarrage — il cree les tables ET applique les migrations
- Ne pas retourner `error` du ToolHandler dans le bridge — utiliser `result.SetError(err)` et retourner `(result, nil)`
- Ne pas modifier directement `Registry.tools` sans prendre le lock — utiliser `LoadTools`
- Ne pas enregistrer une `GoFunc` apres l'appel a `Bridge()` — le bridge snapshot les outils au moment de l'enregistrement

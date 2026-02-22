# mcprt — dynamic MCP tool registry

`mcprt` stores MCP tool definitions in SQLite and hot-reloads them via
`PRAGMA data_version`. Tools can execute SQL queries, SQL scripts, or Go
functions — all defined in database rows.

```
┌──────────────────────┐       ┌───────────────┐
│  mcp_tools_registry  │──────►│   Registry    │──► MCP Server
│  (SQLite table)      │ watch │   (in-memory) │    (via Bridge)
└──────────────────────┘       └───────────────┘
```

## Quick start

```go
reg := mcprt.NewRegistry(db)
reg.Init()
reg.RegisterGoFunc("compute_stats", statsFunc)
go reg.RunWatcher(ctx)

// Bridge registers all active tools into an MCP server.
mcprt.Bridge(mcpServer, reg)
```

## Handler types

| Type | Config | Description |
|------|--------|-------------|
| `sql_query` | `{"query": "SELECT ..."}` | Execute SELECT, return JSON array |
| `sql_script` | `{"statements": [...]}` | Execute multiple statements, optional transaction |
| `go_function` | — | Call a pre-registered Go function |

### SQL query example

```json
{
  "tool_name": "list_users",
  "handler_type": "sql_query",
  "handler_config": "{\"query\": \"SELECT id, name FROM users WHERE role = ?\", \"params\": [\"role\"]}",
  "input_schema": "{\"type\": \"object\", \"properties\": {\"role\": {\"type\": \"string\"}}}"
}
```

### Template parameters

SQL handlers support template expressions: `{{uuid()}}`, `{{now()}}`, and
`{{param_name}}` for parameter references.

## Schema

```sql
CREATE TABLE mcp_tools_registry (
    tool_name      TEXT PRIMARY KEY,
    tool_category  TEXT,
    description    TEXT,
    input_schema   TEXT,         -- JSON Schema
    handler_type   TEXT NOT NULL, -- sql_query, sql_script, go_function
    handler_config TEXT,         -- JSON
    is_active      TEXT NOT NULL DEFAULT 'true',
    created_at     INTEGER,
    updated_at     INTEGER,
    created_by     TEXT,
    version        INTEGER DEFAULT 1
);

CREATE TABLE mcp_tools_history (
    history_id   INTEGER PRIMARY KEY AUTOINCREMENT,
    tool_name    TEXT, version INTEGER,
    changed_by   TEXT, changed_at INTEGER, change_reason TEXT
);
```

## Exported API

| Symbol | Description |
|--------|-------------|
| `Registry` | Tool store with hot-reload |
| `NewRegistry(db, opts)` | Create registry |
| `DynamicTool` | Tool definition loaded from DB |
| `RegisterGoFunc(name, fn)` | Register a Go handler |
| `Bridge(srv, reg)` | Register all tools into MCP server |

╔═══════════════════════════════════════════════════════════════════════════════╗
║  mcprt — Dynamic MCP tool runtime: SQLite registry, hot-reload, RBAC bridge ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  FLOW                                                                        ║
║  ~~~~~                                                                       ║
║                                                                              ║
║  ┌─────────────────┐    ┌────────────────┐    ┌──────────────────┐           ║
║  │ mcp_tools_       │    │   Registry      │    │   mcp.Server     │           ║
║  │ registry (SQLite)│───>│   .LoadTools()  │───>│   (SDK v1.3.1)   │           ║
║  └────────┬────────┘    │   in-memory map │    │   tools/list     │           ║
║           │              └───────┬────────┘    │   tools/call     │           ║
║   PRAGMA data_version           │              └────────┬─────────┘           ║
║   poll (5s interval)            │                       │                     ║
║           │              ┌──────v────────┐       CallToolRequest              ║
║           └─────────────>│  RunWatcher   │              │                     ║
║            auto-reload   │  (hot-reload) │              v                     ║
║                          └───────────────┘    ┌─────────────────────┐         ║
║                                               │  Bridge() pipeline  │         ║
║  ┌──────────┐                                 │                     │         ║
║  │ GoFunc   │──────────────────────────────── │ 1. Group isolation  │         ║
║  │ registry │ (pre-registered Go functions)   │ 2. Policy check     │         ║
║  └──────────┘                                 │ 3. Parse arguments  │         ║
║                                               │ 4. Per-tool timeout │         ║
║                                               │ 5. Execute handler  │         ║
║                                               │ 6. Audit hook       │         ║
║                                               └──────┬──────────────┘         ║
║                                                      │                        ║
║                              ┌────────────────┬──────┴────────────┐           ║
║                              v                v                   v           ║
║                    ┌──────────────┐  ┌──────────────┐  ┌───────────────┐      ║
║                    │ SQLQuery     │  │ SQLScript    │  │ GoFunction    │      ║
║                    │ Handler      │  │ Handler      │  │ Handler       │      ║
║                    │ (SELECT)     │  │ (multi-stmt) │  │ (registered)  │      ║
║                    │ max 10k rows │  │ txn support  │  │               │      ║
║                    │ JSON array/  │  │ {{uuid()}}   │  │ GoFunc(ctx,   │      ║
║                    │ object out   │  │ {{now()}}    │  │   params) str │      ║
║                    └──────────────┘  │ {{param}}    │  └───────────────┘      ║
║                                      └──────────────┘                         ║
║                                                                              ║
║  ┌──────────────────┐          ┌──────────────┐                              ║
║  │ Sanitizer        │──────── │ Bridge opts  │                               ║
║  │ (prompt inject   │          │ WithPolicy   │                               ║
║  │  protection)     │          │ WithAudit    │                               ║
║  │ NFC normalize    │          │ WithSanitizer│                               ║
║  │ strip HTML       │          │ WithGroupIso │                               ║
║  │ strip zero-width │          │ WithTimeout  │                               ║
║  │ strip injection  │          └──────────────┘                              ║
║  │ custom filters   │                                                        ║
║  │ max desc len     │                                                        ║
║  └──────────────────┘                                                        ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  DATABASE TABLES (SQLite)                                                    ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  mcp_tools_registry                                                          ║
║  ├── tool_name TEXT PK                                                       ║
║  ├── tool_category TEXT NOT NULL                                             ║
║  ├── description TEXT NOT NULL                                               ║
║  ├── input_schema TEXT NOT NULL          (JSON: must have "type":"object")   ║
║  ├── handler_type TEXT NOT NULL          ('sql_query'|'sql_script'|'go_func')║
║  ├── handler_config TEXT NOT NULL        (JSON: query/statements config)     ║
║  ├── mode TEXT DEFAULT 'readwrite'       ('readonly'|'readwrite')            ║
║  ├── is_active INTEGER DEFAULT 1                                             ║
║  ├── created_at INTEGER (epoch)                                              ║
║  ├── updated_at INTEGER                                                      ║
║  ├── created_by TEXT                                                         ║
║  ├── version INTEGER DEFAULT 1           (auto-incremented by trigger)       ║
║  ├── group_tag TEXT DEFAULT 'default'    (group isolation tag)               ║
║  └── timeout_ms INTEGER DEFAULT 30000    (per-tool timeout)                  ║
║                                                                              ║
║  mcp_tools_history                        (auto-populated by triggers)       ║
║  ├── history_id INTEGER PK AUTOINCREMENT                                     ║
║  ├── tool_name, tool_category, description, input_schema                    ║
║  ├── handler_type, handler_config, mode, version                            ║
║  ├── changed_by TEXT, changed_at INTEGER, change_reason TEXT                 ║
║  IDX: (tool_name, version DESC)                                              ║
║                                                                              ║
║  mcp_tool_policy                          (RBAC access control)              ║
║  ├── policy_id INTEGER PK AUTOINCREMENT                                      ║
║  ├── tool_name TEXT NOT NULL                                                 ║
║  ├── role TEXT DEFAULT '*'               (wildcard = all roles)              ║
║  ├── effect TEXT DEFAULT 'allow'         ('allow'|'deny')                    ║
║  └── created_at INTEGER                                                      ║
║  IDX: (tool_name, role)                                                      ║
║                                                                              ║
║  TRIGGERS:                                                                   ║
║  - trg_mcp_tools_updated_at: ON UPDATE -> version++, updated_at, snapshot   ║
║  - trg_mcp_tools_insert_history: ON INSERT -> snapshot to history            ║
║  (Requires recursive_triggers = OFF, which is SQLite default)               ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED TYPES                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  DynamicTool       struct  Tool definition loaded from DB                    ║
║  Registry          struct  In-memory tool cache + DB + watcher + GoFuncs     ║
║  GoFunc            func    func(ctx, map[string]any) (string, error)         ║
║  ToolHandler       iface   Execute(ctx, *DynamicTool, params) (string, err)  ║
║  PolicyFunc        func    func(ctx, toolName) error                         ║
║  AuditFunc         func    func(ctx, name, ver, params, result, err, dur)    ║
║  SQLQueryHandler   struct  DB field; SELECT -> JSON                          ║
║  SQLScriptHandler  struct  DB + NewID; multi-stmt transactional              ║
║  DBPolicy          struct  RBAC evaluator from mcp_tool_policy table         ║
║  Sanitizer         struct  Anti-injection cleaner for tool metadata           ║
║  BridgeOption      func    Functional option for Bridge()                     ║
║  RegistryOption    func    Functional option for NewRegistry()                ║
║  SanitizerOption   func    Functional option for DefaultSanitizer()           ║
║                                                                              ║
║  Constants:                                                                  ║
║  - ModeReadonly = "readonly"    ModeReadWrite = "readwrite"                  ║
║  - HandlerSQLQuery  HandlerSQLScript  HandlerGoFunction                      ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  KEY FUNCTIONS                                                               ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  NewRegistry(db, ...RegistryOption) *Registry                                ║
║  Registry.Init() error                    -- creates tables + migrations     ║
║  Registry.LoadTools(ctx) error            -- reload from DB into memory      ║
║  Registry.ListTools() []*DynamicTool                                         ║
║  Registry.GetTool(name) (*DynamicTool, bool)                                 ║
║  Registry.ExecuteTool(ctx, name, params) (string, error)                     ║
║  Registry.RegisterGoFunc(name, GoFunc)                                       ║
║  Registry.RunWatcher(ctx)                 -- PRAGMA data_version poll 5s     ║
║                                                                              ║
║  Bridge(srv, reg, ...BridgeOption)        -- wire all tools to mcp.Server    ║
║  WithPolicy(PolicyFunc) BridgeOption                                         ║
║  WithAudit(AuditFunc) BridgeOption                                           ║
║  WithSanitizer(*Sanitizer) BridgeOption                                      ║
║  WithGroupIsolation(map[string][]string) BridgeOption                        ║
║  WithTimeoutFromDB() BridgeOption                                            ║
║                                                                              ║
║  NewDBPolicy(db) PolicyFunc               -- RBAC from mcp_tool_policy       ║
║  DefaultSanitizer(...SanitizerOption) *Sanitizer                             ║
║                                                                              ║
║  WithGroupSession(ctx, group) context.Context                                ║
║  GetGroupSession(ctx) string                                                 ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES (internal to hazyhaar/pkg)                                     ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  github.com/hazyhaar/pkg/idgen   -- UUID v7 generation                      ║
║  github.com/hazyhaar/pkg/watch   -- PRAGMA data_version polling             ║
║  github.com/hazyhaar/pkg/kit     -- GetRole(ctx) for RBAC                   ║
║                                                                              ║
║  External:                                                                   ║
║  github.com/modelcontextprotocol/go-sdk/mcp  -- MCP SDK v1.3.1              ║
║  golang.org/x/text/unicode/norm              -- NFC normalization            ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  DATA FORMATS                                                                ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  handler_config for sql_query:                                               ║
║    {"query": "SELECT ...", "params": ["p1","p2"], "result_format": "array"}  ║
║                                                                              ║
║  handler_config for sql_script:                                              ║
║    {"statements": [{"sql":"...", "params":["{{uuid()}}","{{name}}"]}],       ║
║     "transaction": true, "return": "last_insert_rowid"|"affected_rows"}      ║
║                                                                              ║
║  Template expressions in sql_script params:                                  ║
║    {{uuid()}} -> idgen UUID v7     {{now()}} -> Unix epoch                  ║
║    {{paramName}} -> user param     unknown fn() -> nil (rejected)            ║
║                                                                              ║
║  Output format: always JSON string                                           ║
║    sql_query:  [{"col":"val",...}] or {"col":"val"} if result_format=object  ║
║    sql_script: {"success":true,"affected_rows":N,"last_insert_id":N}        ║
║    go_function: arbitrary string from GoFunc                                 ║
║                                                                              ║
║  POLICY EVALUATION (DBPolicy):                                               ║
║    deny match  -> DENY     allow exists but no match -> DENY                ║
║    no rules    -> ALLOW    allow match -> ALLOW                              ║
║                                                                              ║
║  GROUP ISOLATION:                                                            ║
║    First call sets session group. Subsequent calls must be in same group     ║
║    or in "default". "default" group is compatible with everything.           ║
║                                                                              ║
║  SANITIZER PIPELINE:                                                         ║
║    NFC normalize -> strip control chars -> strip HTML -> strip injection     ║
║    patterns -> custom filters -> truncate to maxDescLen (default 1024)       ║
║                                                                              ║
╚═══════════════════════════════════════════════════════════════════════════════╝

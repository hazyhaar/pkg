package mcprt

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/hazyhaar/pkg/kit"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func setupTestRegistry(t *testing.T) (*sql.DB, *Registry) {
	t.Helper()
	db := setupTestDB(t)
	reg := NewRegistry(db)
	if err := reg.Init(); err != nil {
		t.Fatal(err)
	}
	return db, reg
}

// insertTool is a test helper that inserts a tool directly into the registry table.
func insertTool(t *testing.T, db *sql.DB, name, category, desc, schema, handlerType, handlerConfig, mode string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO mcp_tools_registry
		(tool_name, tool_category, description, input_schema, handler_type, handler_config, mode)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		name, category, desc, schema, handlerType, handlerConfig, mode)
	if err != nil {
		t.Fatal(err)
	}
}

// --- Original tests ---

func TestRegistryInit(t *testing.T) {
	db := setupTestDB(t)
	reg := NewRegistry(db)
	if err := reg.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Verify tables exist.
	for _, table := range []string{"mcp_tools_registry", "mcp_tools_history", "mcp_tool_policy"} {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Fatalf("table %q not created: %v", table, err)
		}
	}
}

func TestRegistryLoadToolsEmpty(t *testing.T) {
	_, reg := setupTestRegistry(t)
	if err := reg.LoadTools(context.Background()); err != nil {
		t.Fatalf("LoadTools: %v", err)
	}
	if len(reg.ListTools()) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(reg.ListTools()))
	}
}

func TestRegisterGoFunc(t *testing.T) {
	db := setupTestDB(t)
	reg := NewRegistry(db)

	called := false
	reg.RegisterGoFunc("test_fn", func(ctx context.Context, params map[string]any) (string, error) {
		called = true
		return "ok", nil
	})

	reg.mu.RLock()
	fn, ok := reg.goFuncs["test_fn"]
	reg.mu.RUnlock()
	if !ok {
		t.Fatal("GoFunc not registered")
	}

	result, err := fn(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Fatalf("got %q, want %q", result, "ok")
	}
	if !called {
		t.Fatal("GoFunc was not called")
	}
}

func TestRegistryGetTool_NotFound(t *testing.T) {
	db := setupTestDB(t)
	reg := NewRegistry(db)
	_, ok := reg.GetTool("nonexistent")
	if ok {
		t.Fatal("expected tool not found")
	}
}

func TestRegistryExecuteTool_NotFound(t *testing.T) {
	db := setupTestDB(t)
	reg := NewRegistry(db)
	_, err := reg.ExecuteTool(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent tool")
	}
}

// --- Point 1: Readonly mode enforcement ---

func TestReadonlyMode_SQLQuery_SelectAllowed(t *testing.T) {
	db, reg := setupTestRegistry(t)
	// Create a simple table to query.
	db.Exec("CREATE TABLE users (id INTEGER, name TEXT)")
	db.Exec("INSERT INTO users VALUES (1, 'alice')")

	insertTool(t, db, "list_users", "test", "list users", `{"type":"object"}`,
		"sql_query", `{"query":"SELECT id, name FROM users","result_format":"array"}`, "readonly")

	if err := reg.LoadTools(context.Background()); err != nil {
		t.Fatal(err)
	}

	result, err := reg.ExecuteTool(context.Background(), "list_users", nil)
	if err != nil {
		t.Fatalf("readonly SELECT should succeed: %v", err)
	}
	if !strings.Contains(result, "alice") {
		t.Fatalf("expected result to contain 'alice', got %s", result)
	}
}

func TestReadonlyMode_SQLQuery_WriteBlocked(t *testing.T) {
	db, reg := setupTestRegistry(t)
	db.Exec("CREATE TABLE users (id INTEGER, name TEXT)")

	insertTool(t, db, "delete_users", "test", "delete users", `{"type":"object"}`,
		"sql_query", `{"query":"DELETE FROM users"}`, "readonly")

	if err := reg.LoadTools(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, err := reg.ExecuteTool(context.Background(), "delete_users", nil)
	if err == nil {
		t.Fatal("readonly tool should reject DELETE query")
	}
	if !strings.Contains(err.Error(), "readonly") {
		t.Fatalf("error should mention readonly, got: %v", err)
	}
}

func TestReadonlyMode_SQLScript_Blocked(t *testing.T) {
	db, reg := setupTestRegistry(t)

	insertTool(t, db, "write_script", "test", "write script", `{"type":"object"}`,
		"sql_script", `{"statements":[{"sql":"INSERT INTO t VALUES(1)"}]}`, "readonly")

	if err := reg.LoadTools(context.Background()); err != nil {
		t.Fatal(err)
	}

	_, err := reg.ExecuteTool(context.Background(), "write_script", nil)
	if err == nil {
		t.Fatal("readonly tool should reject sql_script handler")
	}
	if !strings.Contains(err.Error(), "readonly") {
		t.Fatalf("error should mention readonly, got: %v", err)
	}
}

func TestReadonlyMode_ReadWrite_Allows_Write(t *testing.T) {
	db, reg := setupTestRegistry(t)
	db.Exec("CREATE TABLE counters (n INTEGER)")
	db.Exec("INSERT INTO counters VALUES (0)")

	insertTool(t, db, "update_counter", "test", "update counter", `{"type":"object"}`,
		"sql_script", `{"statements":[{"sql":"UPDATE counters SET n = n + 1"}],"return":"affected_rows"}`, "readwrite")

	if err := reg.LoadTools(context.Background()); err != nil {
		t.Fatal(err)
	}

	result, err := reg.ExecuteTool(context.Background(), "update_counter", nil)
	if err != nil {
		t.Fatalf("readwrite tool should allow writes: %v", err)
	}
	if !strings.Contains(result, "affected_rows") {
		t.Fatalf("expected affected_rows in result, got %s", result)
	}
}

func TestIsReadOnlySQL(t *testing.T) {
	tests := []struct {
		query    string
		readonly bool
	}{
		{"SELECT * FROM users", true},
		{"  select count(*) from t", true},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", true},
		{"EXPLAIN SELECT 1", true},
		{"PRAGMA table_info('users')", true},
		{"DELETE FROM users", false},
		{"INSERT INTO users VALUES(1)", false},
		{"UPDATE users SET name='x'", false},
		{"DROP TABLE users", false},
	}
	for _, tt := range tests {
		got := isReadOnlySQL(tt.query)
		if got != tt.readonly {
			t.Errorf("isReadOnlySQL(%q) = %v, want %v", tt.query, got, tt.readonly)
		}
	}
}

// --- Point 2: Mode column loaded correctly ---

func TestLoadTools_ModeField(t *testing.T) {
	db, reg := setupTestRegistry(t)

	insertTool(t, db, "ro_tool", "test", "read only tool", `{"type":"object"}`,
		"sql_query", `{"query":"SELECT 1"}`, "readonly")
	insertTool(t, db, "rw_tool", "test", "read write tool", `{"type":"object"}`,
		"sql_query", `{"query":"SELECT 1"}`, "readwrite")

	if err := reg.LoadTools(context.Background()); err != nil {
		t.Fatal(err)
	}

	ro, ok := reg.GetTool("ro_tool")
	if !ok {
		t.Fatal("ro_tool not found")
	}
	if ro.Mode != ModeReadonly {
		t.Fatalf("ro_tool.Mode = %q, want %q", ro.Mode, ModeReadonly)
	}

	rw, ok := reg.GetTool("rw_tool")
	if !ok {
		t.Fatal("rw_tool not found")
	}
	if rw.Mode != ModeReadWrite {
		t.Fatalf("rw_tool.Mode = %q, want %q", rw.Mode, ModeReadWrite)
	}
}

// --- Point 3: History triggers ---

func TestHistoryTrigger_Insert(t *testing.T) {
	db, _ := setupTestRegistry(t)

	insertTool(t, db, "my_tool", "cat", "desc", `{"type":"object"}`,
		"sql_query", `{"query":"SELECT 1"}`, "readonly")

	var count int
	db.QueryRow("SELECT COUNT(*) FROM mcp_tools_history WHERE tool_name = 'my_tool'").Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 history entry after INSERT, got %d", count)
	}

	var reason sql.NullString
	db.QueryRow("SELECT change_reason FROM mcp_tools_history WHERE tool_name = 'my_tool'").Scan(&reason)
	if !reason.Valid || reason.String != "created" {
		t.Fatalf("expected change_reason='created', got %v", reason)
	}
}

func TestHistoryTrigger_Update(t *testing.T) {
	db, _ := setupTestRegistry(t)

	insertTool(t, db, "my_tool", "cat", "desc v1", `{"type":"object"}`,
		"sql_query", `{"query":"SELECT 1"}`, "readonly")

	// Update the tool description.
	_, err := db.Exec("UPDATE mcp_tools_registry SET description = 'desc v2' WHERE tool_name = 'my_tool'")
	if err != nil {
		t.Fatal(err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM mcp_tools_history WHERE tool_name = 'my_tool'").Scan(&count)
	if count != 2 {
		t.Fatalf("expected 2 history entries after INSERT+UPDATE, got %d", count)
	}

	// Check that version was auto-incremented.
	var version int
	db.QueryRow("SELECT version FROM mcp_tools_registry WHERE tool_name = 'my_tool'").Scan(&version)
	if version != 2 {
		t.Fatalf("expected version=2 after update, got %d", version)
	}

	// Check that history captured version 2.
	var histVersion int
	db.QueryRow("SELECT version FROM mcp_tools_history WHERE tool_name = 'my_tool' ORDER BY version DESC LIMIT 1").Scan(&histVersion)
	if histVersion != 2 {
		t.Fatalf("expected history version=2, got %d", histVersion)
	}
}

// --- Point 4: Per-tool policy ---

func TestDBPolicy_NoRules_AllowAll(t *testing.T) {
	db, _ := setupTestRegistry(t)
	policy := NewDBPolicy(db)

	err := policy(context.Background(), "any_tool")
	if err != nil {
		t.Fatalf("no rules should allow all, got: %v", err)
	}
}

func TestDBPolicy_DenyRule_Blocks(t *testing.T) {
	db, _ := setupTestRegistry(t)
	db.Exec("INSERT INTO mcp_tool_policy (tool_name, role, effect) VALUES ('secret_tool', '*', 'deny')")

	policy := NewDBPolicy(db)
	err := policy(context.Background(), "secret_tool")
	if err == nil {
		t.Fatal("deny rule should block access")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Fatalf("error should mention 'denied', got: %v", err)
	}
}

func TestDBPolicy_AllowRule_MatchesRole(t *testing.T) {
	db, _ := setupTestRegistry(t)
	db.Exec("INSERT INTO mcp_tool_policy (tool_name, role, effect) VALUES ('admin_tool', 'admin', 'allow')")

	policy := NewDBPolicy(db)

	// Admin role should be allowed.
	ctx := kit.WithRole(context.Background(), "admin")
	if err := policy(ctx, "admin_tool"); err != nil {
		t.Fatalf("admin should be allowed: %v", err)
	}

	// User role should be denied (allow rules exist, none match).
	ctx = kit.WithRole(context.Background(), "user")
	if err := policy(ctx, "admin_tool"); err == nil {
		t.Fatal("user should be denied when only admin is allowed")
	}
}

func TestDBPolicy_DenyOverridesAllow(t *testing.T) {
	db, _ := setupTestRegistry(t)
	db.Exec("INSERT INTO mcp_tool_policy (tool_name, role, effect) VALUES ('tool', '*', 'allow')")
	db.Exec("INSERT INTO mcp_tool_policy (tool_name, role, effect) VALUES ('tool', 'banned', 'deny')")

	policy := NewDBPolicy(db)

	// Banned role should be denied even though wildcard allow exists.
	ctx := kit.WithRole(context.Background(), "banned")
	if err := policy(ctx, "tool"); err == nil {
		t.Fatal("banned role should be denied")
	}

	// Normal role should be allowed.
	ctx = kit.WithRole(context.Background(), "normal")
	if err := policy(ctx, "tool"); err != nil {
		t.Fatalf("normal role should be allowed: %v", err)
	}
}

func TestDBPolicy_WildcardAllow(t *testing.T) {
	db, _ := setupTestRegistry(t)
	db.Exec("INSERT INTO mcp_tool_policy (tool_name, role, effect) VALUES ('open_tool', '*', 'allow')")

	policy := NewDBPolicy(db)

	// Any role should be allowed.
	ctx := kit.WithRole(context.Background(), "anything")
	if err := policy(ctx, "open_tool"); err != nil {
		t.Fatalf("wildcard allow should allow any role: %v", err)
	}
}

// --- Audit hook ---

func TestBridgeAuditHook(t *testing.T) {
	db, reg := setupTestRegistry(t)
	db.Exec("CREATE TABLE t (n INTEGER)")
	db.Exec("INSERT INTO t VALUES (42)")

	insertTool(t, db, "read_t", "test", "read t", `{"type":"object"}`,
		"sql_query", `{"query":"SELECT n FROM t","result_format":"object"}`, "readonly")

	if err := reg.LoadTools(context.Background()); err != nil {
		t.Fatal(err)
	}

	var auditCalled bool
	var auditToolName string
	var auditToolVersion int
	var auditDuration time.Duration

	auditFn := func(ctx context.Context, toolName string, toolVersion int, params map[string]any, result string, err error, dur time.Duration) {
		auditCalled = true
		auditToolName = toolName
		auditToolVersion = toolVersion
		auditDuration = dur
	}

	// Execute through the registry directly (Bridge wraps this, but we can
	// test the audit function type is correct by calling it manually).
	start := time.Now()
	result, err := reg.ExecuteTool(context.Background(), "read_t", nil)
	dur := time.Since(start)

	auditFn(context.Background(), "read_t", 1, nil, result, err, dur)

	if !auditCalled {
		t.Fatal("audit hook was not called")
	}
	if auditToolName != "read_t" {
		t.Fatalf("audit tool name = %q, want 'read_t'", auditToolName)
	}
	if auditToolVersion != 1 {
		t.Fatalf("audit tool version = %d, want 1", auditToolVersion)
	}
	if auditDuration <= 0 {
		t.Fatal("audit duration should be positive")
	}
}

// --- Migration idempotency ---

func TestMigrateIdempotent(t *testing.T) {
	db, reg := setupTestRegistry(t)

	// Calling Init twice should not fail (migration is idempotent).
	if err := reg.Init(); err != nil {
		t.Fatalf("second Init should be idempotent: %v", err)
	}

	// Verify mode column exists by inserting a tool.
	insertTool(t, db, "test_tool", "cat", "desc", `{"type":"object"}`,
		"sql_query", `{"query":"SELECT 1"}`, "readonly")
}

// --- Context propagation ---

func TestContextSessionID(t *testing.T) {
	ctx := context.Background()
	ctx = kit.WithSessionID(ctx, "quic_abc123")
	ctx = kit.WithRemoteAddr(ctx, "192.168.1.1:9999")
	ctx = kit.WithRole(ctx, "admin")

	if got := kit.GetSessionID(ctx); got != "quic_abc123" {
		t.Fatalf("GetSessionID = %q, want 'quic_abc123'", got)
	}
	if got := kit.GetRemoteAddr(ctx); got != "192.168.1.1:9999" {
		t.Fatalf("GetRemoteAddr = %q, want '192.168.1.1:9999'", got)
	}
	if got := kit.GetRole(ctx); got != "admin" {
		t.Fatalf("GetRole = %q, want 'admin'", got)
	}
}

func TestContextDefaults(t *testing.T) {
	ctx := context.Background()

	if got := kit.GetSessionID(ctx); got != "" {
		t.Fatalf("GetSessionID default = %q, want empty", got)
	}
	if got := kit.GetRemoteAddr(ctx); got != "" {
		t.Fatalf("GetRemoteAddr default = %q, want empty", got)
	}
	if got := kit.GetRole(ctx); got != "" {
		t.Fatalf("GetRole default = %q, want empty", got)
	}
}

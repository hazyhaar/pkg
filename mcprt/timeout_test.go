package mcprt

import (
	"context"
	"testing"

	_ "modernc.org/sqlite"
)

func TestTimeoutMs_LoadedFromDB(t *testing.T) {
	db, reg := setupTestRegistry(t)

	// Insert tool with custom timeout.
	_, err := db.Exec(`INSERT INTO mcp_tools_registry
		(tool_name, tool_category, description, input_schema, handler_type, handler_config, mode, timeout_ms)
		VALUES ('fast_tool', 'test', 'fast tool', '{"type":"object"}', 'sql_query', '{"query":"SELECT 1"}', 'readonly', 5000)`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert tool with default timeout.
	insertTool(t, db, "default_tool", "test", "default tool", "sql_query", `{"query":"SELECT 1"}`, "readonly")

	if err := reg.LoadTools(context.Background()); err != nil {
		t.Fatal(err)
	}

	fast, ok := reg.GetTool("fast_tool")
	if !ok {
		t.Fatal("fast_tool not found")
	}
	if fast.TimeoutMs != 5000 {
		t.Fatalf("fast_tool.TimeoutMs = %d, want 5000", fast.TimeoutMs)
	}

	def, ok := reg.GetTool("default_tool")
	if !ok {
		t.Fatal("default_tool not found")
	}
	if def.TimeoutMs != 30000 {
		t.Fatalf("default_tool.TimeoutMs = %d, want 30000", def.TimeoutMs)
	}
}

func TestGroupTag_LoadedFromDB(t *testing.T) {
	db, reg := setupTestRegistry(t)

	// Insert tool with custom group tag.
	_, err := db.Exec(`INSERT INTO mcp_tools_registry
		(tool_name, tool_category, description, input_schema, handler_type, handler_config, mode, group_tag)
		VALUES ('secret_tool', 'test', 'secret tool', '{"type":"object"}', 'sql_query', '{"query":"SELECT 1"}', 'readonly', 'sensitive')`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert tool with default group tag.
	insertTool(t, db, "normal_tool", "test", "normal tool", "sql_query", `{"query":"SELECT 1"}`, "readonly")

	if err := reg.LoadTools(context.Background()); err != nil {
		t.Fatal(err)
	}

	secret, ok := reg.GetTool("secret_tool")
	if !ok {
		t.Fatal("secret_tool not found")
	}
	if secret.GroupTag != "sensitive" {
		t.Fatalf("secret_tool.GroupTag = %q, want 'sensitive'", secret.GroupTag)
	}

	normal, ok := reg.GetTool("normal_tool")
	if !ok {
		t.Fatal("normal_tool not found")
	}
	if normal.GroupTag != "default" {
		t.Fatalf("normal_tool.GroupTag = %q, want 'default'", normal.GroupTag)
	}
}

func TestWithTimeoutFromDB_BridgeOption(t *testing.T) {
	opt := WithTimeoutFromDB()
	var cfg bridgeConfig
	opt(&cfg)
	if !cfg.timeoutDB {
		t.Fatal("timeoutDB should be true")
	}
}

func TestMigrate_AddsNewColumns(t *testing.T) {
	db := setupTestDB(t)
	reg := NewRegistry(db)
	if err := reg.Init(); err != nil {
		t.Fatal(err)
	}

	// Verify new columns exist by inserting with them.
	_, err := db.Exec(`INSERT INTO mcp_tools_registry
		(tool_name, tool_category, description, input_schema, handler_type, handler_config, group_tag, timeout_ms)
		VALUES ('test', 'cat', 'desc', '{"type":"object"}', 'sql_query', '{"query":"SELECT 1"}', 'mygroup', 15000)`)
	if err != nil {
		t.Fatalf("new columns should exist: %v", err)
	}

	var groupTag string
	var timeoutMs int
	err = db.QueryRow("SELECT group_tag, timeout_ms FROM mcp_tools_registry WHERE tool_name = 'test'").
		Scan(&groupTag, &timeoutMs)
	if err != nil {
		t.Fatal(err)
	}
	if groupTag != "mygroup" {
		t.Fatalf("group_tag = %q, want 'mygroup'", groupTag)
	}
	if timeoutMs != 15000 {
		t.Fatalf("timeout_ms = %d, want 15000", timeoutMs)
	}
}

func TestMigrate_Idempotent_WithNewColumns(t *testing.T) {
	db := setupTestDB(t)
	reg := NewRegistry(db)

	// First init.
	if err := reg.Init(); err != nil {
		t.Fatal(err)
	}
	// Second init — should be idempotent.
	if err := reg.Init(); err != nil {
		t.Fatalf("second Init should be idempotent: %v", err)
	}
}


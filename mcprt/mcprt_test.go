package mcprt

import (
	"context"
	"database/sql"
	"testing"

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

func TestRegistryInit(t *testing.T) {
	db := setupTestDB(t)
	reg := NewRegistry(db)
	if err := reg.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Verify table exists
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='mcp_tools_registry'").Scan(&name)
	if err != nil {
		t.Fatalf("table not created: %v", err)
	}
}

func TestRegistryLoadToolsEmpty(t *testing.T) {
	db := setupTestDB(t)
	reg := NewRegistry(db)
	if err := reg.Init(); err != nil {
		t.Fatal(err)
	}
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

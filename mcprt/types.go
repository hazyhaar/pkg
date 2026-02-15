package mcprt

import (
	"context"
	"database/sql"
	"sync"

	"github.com/hazyhaar/pkg/idgen"
	"github.com/hazyhaar/pkg/watch"
)

// DynamicTool is a tool loaded from the mcp_tools_registry table.
type DynamicTool struct {
	Name          string
	Category      string
	Description   string
	InputSchema   map[string]any
	HandlerType   string
	HandlerConfig map[string]any
	Version       int
	IsActive      bool
}

// ToolHandler executes a dynamic tool with the given parameters.
type ToolHandler interface {
	Execute(ctx context.Context, tool *DynamicTool, params map[string]any) (string, error)
}

// GoFunc is a pre-registered Go function callable from a dynamic tool.
type GoFunc func(ctx context.Context, params map[string]any) (string, error)

// Registry holds loaded tools in memory with a watcher for hot reload.
type Registry struct {
	db      *sql.DB
	newID   idgen.Generator
	tools   map[string]*DynamicTool
	goFuncs map[string]GoFunc
	watcher *watch.Watcher
	mu      sync.RWMutex
}

const (
	HandlerSQLQuery    = "sql_query"
	HandlerSQLScript   = "sql_script"
	HandlerGoFunction  = "go_function"
)

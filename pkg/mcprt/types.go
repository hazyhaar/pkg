package mcprt

import (
	"context"
	"database/sql"
	"sync"

	"github.com/hazyhaar/pkg/pkg/idgen"
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

// Registry holds loaded tools in memory with a watcher for hot reload.
type Registry struct {
	db          *sql.DB
	newID       idgen.Generator
	tools       map[string]*DynamicTool
	lastVersion int64
	mu          sync.RWMutex
}

const (
	HandlerSQLQuery  = "sql_query"
	HandlerSQLScript = "sql_script"
)

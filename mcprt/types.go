package mcprt

import (
	"context"
	"database/sql"
	"sync"
	"time"

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
	Mode          string // "readonly" or "readwrite"
	Version       int
	IsActive      bool
}

// ModeReadonly and ModeReadWrite are the valid values for DynamicTool.Mode.
const (
	ModeReadonly  = "readonly"
	ModeReadWrite = "readwrite"
)

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
	HandlerSQLQuery   = "sql_query"
	HandlerSQLScript  = "sql_script"
	HandlerGoFunction = "go_function"
)

// PolicyFunc decides whether a tool call is allowed.
// Return nil to allow, non-nil error to deny.
type PolicyFunc func(ctx context.Context, toolName string) error

// AuditFunc records a tool execution for observability.
// toolVersion captures which version of the tool definition ran.
type AuditFunc func(ctx context.Context, toolName string, toolVersion int, params map[string]any, result string, err error, duration time.Duration)

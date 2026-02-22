package mcprt

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hazyhaar/pkg/idgen"
	"github.com/hazyhaar/pkg/watch"
)

const Schema = `
CREATE TABLE IF NOT EXISTS mcp_tools_registry (
	tool_name TEXT PRIMARY KEY,
	tool_category TEXT NOT NULL,
	description TEXT NOT NULL,
	input_schema TEXT NOT NULL,
	handler_type TEXT NOT NULL CHECK(handler_type IN ('sql_query', 'sql_script', 'go_function')),
	handler_config TEXT NOT NULL,
	mode TEXT NOT NULL DEFAULT 'readwrite' CHECK(mode IN ('readonly', 'readwrite')),
	is_active INTEGER NOT NULL DEFAULT 1 CHECK(is_active IN (0, 1)),
	created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
	updated_at INTEGER,
	created_by TEXT,
	version INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_mcp_tools_active ON mcp_tools_registry(is_active);

CREATE TABLE IF NOT EXISTS mcp_tools_history (
	history_id INTEGER PRIMARY KEY AUTOINCREMENT,
	tool_name TEXT NOT NULL,
	tool_category TEXT NOT NULL,
	description TEXT NOT NULL,
	input_schema TEXT NOT NULL,
	handler_type TEXT NOT NULL,
	handler_config TEXT NOT NULL,
	mode TEXT NOT NULL DEFAULT 'readwrite',
	version INTEGER NOT NULL,
	changed_by TEXT,
	changed_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
	change_reason TEXT
);
CREATE INDEX IF NOT EXISTS idx_mcp_history_tool ON mcp_tools_history(tool_name, version DESC);

CREATE TABLE IF NOT EXISTS mcp_tool_policy (
	policy_id INTEGER PRIMARY KEY AUTOINCREMENT,
	tool_name TEXT NOT NULL,
	role TEXT NOT NULL DEFAULT '*',
	effect TEXT NOT NULL DEFAULT 'allow' CHECK(effect IN ('allow', 'deny')),
	created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_mcp_policy_tool ON mcp_tool_policy(tool_name, role);

-- Consolidated trigger: auto-increment version, set updated_at, snapshot to history.
-- Relies on recursive_triggers = OFF (SQLite default) to avoid re-triggering.
DROP TRIGGER IF EXISTS trg_mcp_tools_updated_at;
CREATE TRIGGER trg_mcp_tools_updated_at
AFTER UPDATE ON mcp_tools_registry
FOR EACH ROW
BEGIN
	UPDATE mcp_tools_registry SET updated_at = strftime('%s', 'now'), version = OLD.version + 1 WHERE tool_name = NEW.tool_name;
	INSERT INTO mcp_tools_history (tool_name, tool_category, description, input_schema, handler_type, handler_config, mode, version)
	VALUES (NEW.tool_name, NEW.tool_category, NEW.description, NEW.input_schema, NEW.handler_type, NEW.handler_config, NEW.mode, OLD.version + 1);
END;

-- Snapshot initial version on insert.
DROP TRIGGER IF EXISTS trg_mcp_tools_insert_history;
CREATE TRIGGER trg_mcp_tools_insert_history
AFTER INSERT ON mcp_tools_registry
FOR EACH ROW
BEGIN
	INSERT INTO mcp_tools_history (tool_name, tool_category, description, input_schema, handler_type, handler_config, mode, version, changed_by, change_reason)
	VALUES (NEW.tool_name, NEW.tool_category, NEW.description, NEW.input_schema, NEW.handler_type, NEW.handler_config, NEW.mode, NEW.version, NEW.created_by, 'created');
END;
`

// RegistryOption configures a Registry.
type RegistryOption func(*Registry)

// WithRegistryIDGenerator sets a custom ID generator for template expansions.
func WithRegistryIDGenerator(gen idgen.Generator) RegistryOption {
	return func(r *Registry) { r.newID = gen }
}

func NewRegistry(db *sql.DB, opts ...RegistryOption) *Registry {
	r := &Registry{
		db:      db,
		newID:   idgen.Default,
		tools:   make(map[string]*DynamicTool),
		goFuncs: make(map[string]GoFunc),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// RegisterGoFunc registers a Go function callable by dynamic tools with handler_type="go_function".
func (r *Registry) RegisterGoFunc(name string, fn GoFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.goFuncs[name] = fn
}

// Init creates the registry tables and applies migrations for existing databases.
func (r *Registry) Init() error {
	if _, err := r.db.Exec(Schema); err != nil {
		return err
	}
	return r.migrate()
}

// migrate adds columns introduced after initial release.
// Idempotent: ignores "duplicate column" errors from ALTER TABLE.
func (r *Registry) migrate() error {
	alters := []string{
		`ALTER TABLE mcp_tools_registry ADD COLUMN mode TEXT NOT NULL DEFAULT 'readwrite'`,
		`ALTER TABLE mcp_tools_history ADD COLUMN mode TEXT NOT NULL DEFAULT 'readwrite'`,
	}
	for _, stmt := range alters {
		if _, err := r.db.Exec(stmt); err != nil {
			if !strings.Contains(err.Error(), "duplicate column") {
				return err
			}
		}
	}
	return nil
}

// LoadTools loads all active tools from the mcp_tools_registry table.
func (r *Registry) LoadTools(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	rows, err := r.db.QueryContext(ctx, `
		SELECT tool_name, tool_category, description, input_schema,
		       handler_type, handler_config, mode, version, is_active
		FROM mcp_tools_registry
		WHERE is_active = 1
		ORDER BY tool_category, tool_name`)
	if err != nil {
		return fmt.Errorf("query registry: %w", err)
	}
	defer rows.Close()

	newTools := make(map[string]*DynamicTool)
	for rows.Next() {
		var t DynamicTool
		var schemaJSON, configJSON string
		if err := rows.Scan(&t.Name, &t.Category, &t.Description,
			&schemaJSON, &t.HandlerType, &configJSON, &t.Mode, &t.Version, &t.IsActive); err != nil {
			return fmt.Errorf("scan tool: %w", err)
		}
		if t.Mode == "" {
			t.Mode = ModeReadWrite
		}
		if err := json.Unmarshal([]byte(schemaJSON), &t.InputSchema); err != nil {
			slog.Warn("bad input_schema, skipping", "tool", t.Name, "error", err)
			continue
		}
		if err := json.Unmarshal([]byte(configJSON), &t.HandlerConfig); err != nil {
			slog.Warn("bad handler_config, skipping", "tool", t.Name, "error", err)
			continue
		}
		newTools[t.Name] = &t
	}

	r.tools = newTools
	slog.Info("dynamic tools loaded", "count", len(newTools))
	return nil
}

func (r *Registry) ListTools() []*DynamicTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*DynamicTool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}

func (r *Registry) GetTool(name string) (*DynamicTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) ExecuteTool(ctx context.Context, toolName string, params map[string]any) (string, error) {
	t, ok := r.GetTool(toolName)
	if !ok {
		return "", fmt.Errorf("tool not found: %s", toolName)
	}

	if required, ok := t.InputSchema["required"].([]any); ok {
		for _, rf := range required {
			name, _ := rf.(string)
			if name == "" {
				continue
			}
			if _, exists := params[name]; !exists {
				return "", fmt.Errorf("missing required param: %s", name)
			}
		}
	}

	// Readonly enforcement: sql_script is inherently a write handler.
	if t.Mode == ModeReadonly && t.HandlerType == HandlerSQLScript {
		return "", fmt.Errorf("tool %q is readonly: sql_script handler not allowed", toolName)
	}

	switch t.HandlerType {
	case HandlerSQLQuery:
		return (&SQLQueryHandler{DB: r.db}).Execute(ctx, t, params)
	case HandlerSQLScript:
		return (&SQLScriptHandler{DB: r.db, NewID: r.newID}).Execute(ctx, t, params)
	case HandlerGoFunction:
		r.mu.RLock()
		fn, ok := r.goFuncs[toolName]
		r.mu.RUnlock()
		if !ok {
			return "", fmt.Errorf("go function not registered: %s", toolName)
		}
		return fn(ctx, params)
	default:
		return "", fmt.Errorf("unsupported handler type: %s", t.HandlerType)
	}
}

// RunWatcher polls for database changes and reloads tools automatically.
// It uses watch.Watcher with PRAGMA data_version detection.
func (r *Registry) RunWatcher(ctx context.Context) {
	w := watch.New(r.db, watch.Options{
		Interval: 5 * time.Second,
		Detector: watch.PragmaDataVersion,
	})
	r.watcher = w
	w.OnChange(ctx, func() error {
		return r.LoadTools(ctx)
	})
}

package mcprt

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/hazyhaar/pkg/pkg/idgen"
)

const Schema = `
CREATE TABLE IF NOT EXISTS mcp_tools_registry (
	tool_name TEXT PRIMARY KEY,
	tool_category TEXT NOT NULL,
	description TEXT NOT NULL,
	input_schema TEXT NOT NULL,
	handler_type TEXT NOT NULL CHECK(handler_type IN ('sql_query', 'sql_script')),
	handler_config TEXT NOT NULL,
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
	version INTEGER NOT NULL,
	changed_by TEXT,
	changed_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
	change_reason TEXT
);
CREATE INDEX IF NOT EXISTS idx_mcp_history_tool ON mcp_tools_history(tool_name, version DESC);

CREATE TRIGGER IF NOT EXISTS trg_mcp_tools_updated_at
AFTER UPDATE ON mcp_tools_registry
FOR EACH ROW
BEGIN
	UPDATE mcp_tools_registry SET updated_at = strftime('%s', 'now') WHERE tool_name = NEW.tool_name;
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
		db:    db,
		newID: idgen.Default,
		tools: make(map[string]*DynamicTool),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Init creates the registry tables.
func (r *Registry) Init() error {
	_, err := r.db.Exec(Schema)
	return err
}

// LoadTools loads all active tools from the mcp_tools_registry table.
func (r *Registry) LoadTools(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	rows, err := r.db.QueryContext(ctx, `
		SELECT tool_name, tool_category, description, input_schema,
		       handler_type, handler_config, version, is_active
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
			&schemaJSON, &t.HandlerType, &configJSON, &t.Version, &t.IsActive); err != nil {
			return fmt.Errorf("scan tool: %w", err)
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

	var handler ToolHandler
	switch t.HandlerType {
	case HandlerSQLQuery:
		handler = &SQLQueryHandler{DB: r.db}
	case HandlerSQLScript:
		handler = &SQLScriptHandler{DB: r.db, NewID: r.newID}
	default:
		return "", fmt.Errorf("unsupported handler type: %s", t.HandlerType)
	}

	return handler.Execute(ctx, t, params)
}

// RunWatcher polls PRAGMA data_version every 5s and reloads tools on change.
func (r *Registry) RunWatcher(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	slog.Info("registry watcher started")
	for {
		select {
		case <-ctx.Done():
			slog.Info("registry watcher stopped")
			return
		case <-ticker.C:
			var ver int64
			if err := r.db.QueryRow("PRAGMA data_version").Scan(&ver); err != nil {
				slog.Warn("data_version poll failed", "error", err)
				continue
			}
			if ver != r.lastVersion && r.lastVersion != 0 {
				slog.Info("registry change detected, reloading")
				if err := r.LoadTools(ctx); err != nil {
					slog.Error("reload failed", "error", err)
				}
			}
			r.lastVersion = ver
		}
	}
}

package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
)

// MCPTool represents a row from the hpx_mcp_tools table.
type MCPTool struct {
	ToolName      string          `json:"tool_name"`
	ToolCategory  string          `json:"tool_category"`
	Description   string          `json:"description"`
	InputSchema   json.RawMessage `json:"input_schema"`
	HandlerType   string          `json:"handler_type"`
	HandlerConfig json.RawMessage `json:"handler_config"`
	IsActive      bool            `json:"is_active"`
	Version       int             `json:"version"`
	CreatedBy     string          `json:"created_by,omitempty"`
	CreatedAt     int64           `json:"created_at,omitempty"`
	UpdatedAt     int64           `json:"updated_at,omitempty"`
}

// RegisterMCPTool inserts or updates a dynamic MCP tool.
func (cp *ControlPlane) RegisterMCPTool(ctx context.Context, t MCPTool) error {
	isActive := 0
	if t.IsActive {
		isActive = 1
	}
	_, err := cp.db.ExecContext(ctx,
		`INSERT INTO hpx_mcp_tools (tool_name, tool_category, description, input_schema,
		        handler_type, handler_config, is_active, version, created_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(tool_name) DO UPDATE SET
		     tool_category = excluded.tool_category,
		     description = excluded.description,
		     input_schema = excluded.input_schema,
		     handler_type = excluded.handler_type,
		     handler_config = excluded.handler_config,
		     is_active = excluded.is_active,
		     version = hpx_mcp_tools.version + 1`,
		t.ToolName, t.ToolCategory, t.Description,
		string(t.InputSchema), t.HandlerType, string(t.HandlerConfig),
		isActive, t.Version, t.CreatedBy)
	if err != nil {
		return fmt.Errorf("controlplane: register mcp tool: %w", err)
	}
	return nil
}

// DeactivateMCPTool marks a tool as inactive without deleting it.
func (cp *ControlPlane) DeactivateMCPTool(ctx context.Context, toolName string) error {
	_, err := cp.db.ExecContext(ctx,
		`UPDATE hpx_mcp_tools SET is_active = 0 WHERE tool_name = ?`, toolName)
	return err
}

// DeleteMCPTool removes a tool from the registry.
func (cp *ControlPlane) DeleteMCPTool(ctx context.Context, toolName string) error {
	_, err := cp.db.ExecContext(ctx,
		`DELETE FROM hpx_mcp_tools WHERE tool_name = ?`, toolName)
	return err
}

// LoadActiveMCPTools loads all active tools into a map keyed by tool name.
func (cp *ControlPlane) LoadActiveMCPTools(ctx context.Context) (map[string]*MCPTool, error) {
	tools := make(map[string]*MCPTool)
	for t, err := range cp.ListMCPTools(ctx) {
		if err != nil {
			return nil, err
		}
		tools[t.ToolName] = &t
	}
	slog.Info("mcp tools loaded", "count", len(tools))
	return tools, nil
}

// ListMCPTools returns an iterator over all active MCP tools.
func (cp *ControlPlane) ListMCPTools(ctx context.Context) iter.Seq2[MCPTool, error] {
	return func(yield func(MCPTool, error) bool) {
		rows, err := cp.db.QueryContext(ctx,
			`SELECT tool_name, tool_category, description, input_schema,
			        handler_type, handler_config, is_active, version,
			        COALESCE(created_by,''), COALESCE(created_at,0), COALESCE(updated_at,0)
			 FROM hpx_mcp_tools WHERE is_active = 1
			 ORDER BY tool_category, tool_name`)
		if err != nil {
			yield(MCPTool{}, fmt.Errorf("controlplane: list mcp tools: %w", err))
			return
		}
		defer rows.Close()

		for rows.Next() {
			var t MCPTool
			var schemaStr, configStr string
			var isActive int
			if err := rows.Scan(&t.ToolName, &t.ToolCategory, &t.Description,
				&schemaStr, &t.HandlerType, &configStr,
				&isActive, &t.Version, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt); err != nil {
				yield(MCPTool{}, fmt.Errorf("controlplane: scan mcp tool: %w", err))
				return
			}
			t.InputSchema = json.RawMessage(schemaStr)
			t.HandlerConfig = json.RawMessage(configStr)
			t.IsActive = isActive == 1
			if !yield(t, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(MCPTool{}, err)
		}
	}
}

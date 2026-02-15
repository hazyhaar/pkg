package mcprt

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Bridge registers all dynamic tools from the registry into an MCP server.
func Bridge(srv *server.MCPServer, reg *Registry) {
	for _, t := range reg.ListTools() {
		registerDynamicTool(srv, reg, t)
	}
}

func registerDynamicTool(srv *server.MCPServer, reg *Registry, t *DynamicTool) {
	schemaJSON, _ := json.Marshal(t.InputSchema)
	tool := mcp.NewToolWithRawSchema(t.Name, t.Description, schemaJSON)

	toolName := t.Name
	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		params := req.GetArguments()
		result, err := reg.ExecuteTool(ctx, toolName, params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("%s: %v", toolName, err)), nil
		}
		return mcp.NewToolResultText(result), nil
	})
}

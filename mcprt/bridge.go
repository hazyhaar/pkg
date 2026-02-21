package mcprt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Bridge registers all dynamic tools from the registry into an MCP server.
func Bridge(srv *mcp.Server, reg *Registry) {
	for _, t := range reg.ListTools() {
		registerDynamicTool(srv, reg, t)
	}
}

func registerDynamicTool(srv *mcp.Server, reg *Registry, t *DynamicTool) {
	tool := &mcp.Tool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: json.RawMessage(mustMarshal(t.InputSchema)),
	}

	toolName := t.Name
	srv.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var params map[string]any
		if req.Params.Arguments != nil {
			if err := json.Unmarshal(req.Params.Arguments, &params); err != nil {
				var res mcp.CallToolResult
				res.SetError(fmt.Errorf("%s: invalid arguments: %w", toolName, err))
				return &res, nil
			}
		}

		result, err := reg.ExecuteTool(ctx, toolName, params)
		if err != nil {
			var res mcp.CallToolResult
			res.SetError(errors.New(fmt.Sprintf("%s: %v", toolName, err)))
			return &res, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil
	})
}

func mustMarshal(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mcprt: marshal input schema: %v", err))
	}
	return data
}

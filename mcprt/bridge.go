package mcprt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Bridge registers all dynamic tools from the registry into an MCP server.
//
// Migration note (feb 2026): uses official SDK's low-level ToolHandler (not
// the generic AddTool). Arguments arrive as json.RawMessage in
// req.Params.Arguments (not pre-decoded map[string]any like mcp-go did).
// InputSchema is set as json.RawMessage on mcp.Tool — must be valid JSON
// with "type":"object" or the SDK may reject it.
// Returning a non-nil error from the handler = JSON-RPC protocol error.
// For tool errors, use result.SetError(err) and return (result, nil).
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

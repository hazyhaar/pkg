package kit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPDecodeResult holds the decoded request and an optional context enrichment.
type MCPDecodeResult struct {
	Request   any
	EnrichCtx func(context.Context) context.Context
}

// RegisterMCPTool registers an Endpoint as an MCP tool on the given server.
// The decode function extracts the typed request from MCP arguments.
//
// Migration note (feb 2026): signature changed from mcp-go to official SDK.
// - srv: *server.MCPServer → *mcp.Server
// - tool: mcp.Tool (value) → *mcp.Tool (pointer)
// - decode param: mcp.CallToolRequest (value) → *mcp.CallToolRequest (pointer)
// - Arguments are now in req.Params.Arguments as json.RawMessage, not map[string]any.
// Callers must update their decode functions accordingly.
func RegisterMCPTool(srv *mcp.Server, tool *mcp.Tool, endpoint Endpoint, decode func(*mcp.CallToolRequest) (*MCPDecodeResult, error)) {
	srv.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		decoded, err := decode(req)
		if err != nil {
			var res mcp.CallToolResult
			res.SetError(fmt.Errorf("invalid arguments: %w", err))
			return &res, nil
		}
		if decoded.EnrichCtx != nil {
			ctx = decoded.EnrichCtx(ctx)
		}

		resp, err := endpoint(ctx, decoded.Request)
		if err != nil {
			var res mcp.CallToolResult
			res.SetError(errors.New(err.Error()))
			return &res, nil
		}

		data, err := json.Marshal(resp)
		if err != nil {
			var res mcp.CallToolResult
			res.SetError(fmt.Errorf("marshal: %w", err))
			return &res, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
		}, nil
	})
}

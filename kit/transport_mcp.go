package kit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// MCPDecodeResult holds the decoded request and an optional context enrichment.
type MCPDecodeResult struct {
	Request   any
	EnrichCtx func(context.Context) context.Context
}

// RegisterMCPTool registers an Endpoint as an MCP tool on the given server.
// The decode function extracts the typed request from MCP arguments.
func RegisterMCPTool(srv *server.MCPServer, tool mcp.Tool, endpoint Endpoint, decode func(mcp.CallToolRequest) (*MCPDecodeResult, error)) {
	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		decoded, err := decode(req)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid arguments: %v", err)), nil
		}
		if decoded.EnrichCtx != nil {
			ctx = decoded.EnrichCtx(ctx)
		}

		resp, err := endpoint(ctx, decoded.Request)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		data, err := json.Marshal(resp)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	})
}

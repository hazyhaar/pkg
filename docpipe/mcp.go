// CLAUDE:SUMMARY Registers docpipe MCP tools (extract, detect, formats) on an MCP server.
package docpipe

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hazyhaar/pkg/kit"
)

// RegisterMCP registers docpipe tools on an MCP server.
func (p *Pipeline) RegisterMCP(srv *mcp.Server) {
	p.registerExtractTool(srv)
	p.registerDetectTool(srv)
	p.registerFormatsTool(srv)
}

func inputSchema(properties map[string]any, required []string) map[string]any {
	s := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// --- extract ---

type extractReq struct {
	Path string `json:"path"`
}

func (p *Pipeline) registerExtractTool(srv *mcp.Server) {
	tool := &mcp.Tool{
		Name:        "docpipe_extract",
		Description: "Extract structured content from a document file (docx, odt, pdf, md, txt, html).",
		InputSchema: inputSchema(map[string]any{
			"path": map[string]any{"type": "string", "description": "File path to extract"},
		}, []string{"path"}),
	}

	endpoint := func(ctx context.Context, req any) (any, error) {
		r := req.(*extractReq)
		return p.Extract(ctx, r.Path)
	}

	decode := func(req *mcp.CallToolRequest) (*kit.MCPDecodeResult, error) {
		var r extractReq
		if err := json.Unmarshal(req.Params.Arguments, &r); err != nil {
			return nil, err
		}
		return &kit.MCPDecodeResult{Request: &r}, nil
	}

	kit.RegisterMCPTool(srv, tool, endpoint, decode)
}

// --- detect ---

type detectReq struct {
	Path string `json:"path"`
}

func (p *Pipeline) registerDetectTool(srv *mcp.Server) {
	tool := &mcp.Tool{
		Name:        "docpipe_detect",
		Description: "Detect the format of a document file from its extension.",
		InputSchema: inputSchema(map[string]any{
			"path": map[string]any{"type": "string", "description": "File path to detect"},
		}, []string{"path"}),
	}

	endpoint := func(_ context.Context, req any) (any, error) {
		r := req.(*detectReq)
		format, err := p.Detect(r.Path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"format": string(format)}, nil
	}

	decode := func(req *mcp.CallToolRequest) (*kit.MCPDecodeResult, error) {
		var r detectReq
		if err := json.Unmarshal(req.Params.Arguments, &r); err != nil {
			return nil, err
		}
		return &kit.MCPDecodeResult{Request: &r}, nil
	}

	kit.RegisterMCPTool(srv, tool, endpoint, decode)
}

// --- formats ---

func (p *Pipeline) registerFormatsTool(srv *mcp.Server) {
	tool := &mcp.Tool{
		Name:        "docpipe_formats",
		Description: "List all supported document formats.",
		InputSchema: inputSchema(map[string]any{}, nil),
	}

	endpoint := func(_ context.Context, _ any) (any, error) {
		return map[string]any{"formats": SupportedFormats()}, nil
	}

	decode := func(_ *mcp.CallToolRequest) (*kit.MCPDecodeResult, error) {
		return &kit.MCPDecodeResult{Request: nil}, nil
	}

	kit.RegisterMCPTool(srv, tool, endpoint, decode)
}

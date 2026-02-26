// CLAUDE:SUMMARY Registers horosembed_embed and horosembed_batch MCP tools via kit.RegisterMCPTool.
// CLAUDE:DEPENDS github.com/hazyhaar/pkg/kit, github.com/modelcontextprotocol/go-sdk/mcp
// CLAUDE:EXPORTS RegisterMCP
package horosembed

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hazyhaar/pkg/kit"
)

// RegisterMCP registers horosembed tools on an MCP server.
func RegisterMCP(srv *mcp.Server, emb Embedder) {
	registerEmbedTool(srv, emb)
	registerBatchTool(srv, emb)
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

// --- embed ---

type embedReq struct {
	Text string `json:"text"`
}

func registerEmbedTool(srv *mcp.Server, emb Embedder) {
	tool := &mcp.Tool{
		Name:        "horosembed_embed",
		Description: "Generate an embedding vector for a single text string.",
		InputSchema: inputSchema(map[string]any{
			"text": map[string]any{"type": "string", "description": "Text to embed"},
		}, []string{"text"}),
	}

	endpoint := func(ctx context.Context, req any) (any, error) {
		r := req.(*embedReq)
		vec, err := emb.Embed(ctx, r.Text)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"vector":    vec,
			"dimension": len(vec),
			"model":     emb.Model(),
		}, nil
	}

	decode := func(req *mcp.CallToolRequest) (*kit.MCPDecodeResult, error) {
		var r embedReq
		if err := json.Unmarshal(req.Params.Arguments, &r); err != nil {
			return nil, err
		}
		return &kit.MCPDecodeResult{Request: &r}, nil
	}

	kit.RegisterMCPTool(srv, tool, endpoint, decode)
}

// --- batch ---

type batchReq struct {
	Texts []string `json:"texts"`
}

func registerBatchTool(srv *mcp.Server, emb Embedder) {
	tool := &mcp.Tool{
		Name:        "horosembed_batch",
		Description: "Generate embedding vectors for multiple texts in one call.",
		InputSchema: inputSchema(map[string]any{
			"texts": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Texts to embed",
			},
		}, []string{"texts"}),
	}

	endpoint := func(ctx context.Context, req any) (any, error) {
		r := req.(*batchReq)
		vecs, err := emb.EmbedBatch(ctx, r.Texts)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"vectors":   vecs,
			"count":     len(vecs),
			"dimension": emb.Dimension(),
			"model":     emb.Model(),
		}, nil
	}

	decode := func(req *mcp.CallToolRequest) (*kit.MCPDecodeResult, error) {
		var r batchReq
		if err := json.Unmarshal(req.Params.Arguments, &r); err != nil {
			return nil, err
		}
		return &kit.MCPDecodeResult{Request: &r}, nil
	}

	kit.RegisterMCPTool(srv, tool, endpoint, decode)
}

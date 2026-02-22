package mcprt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// BridgeOption configures Bridge behavior.
type BridgeOption func(*bridgeConfig)

type bridgeConfig struct {
	policy PolicyFunc
	audit  AuditFunc
}

// WithPolicy adds a policy check before each tool execution.
func WithPolicy(fn PolicyFunc) BridgeOption {
	return func(c *bridgeConfig) { c.policy = fn }
}

// WithAudit adds an audit hook called after each tool execution.
func WithAudit(fn AuditFunc) BridgeOption {
	return func(c *bridgeConfig) { c.audit = fn }
}

// Bridge registers all dynamic tools from the registry into an MCP server.
//
// Migration note (feb 2026): uses official SDK's low-level ToolHandler (not
// the generic AddTool). Arguments arrive as json.RawMessage in
// req.Params.Arguments (not pre-decoded map[string]any like mcp-go did).
// InputSchema is set as json.RawMessage on mcp.Tool — must be valid JSON
// with "type":"object" or the SDK may reject it.
// Returning a non-nil error from the handler = JSON-RPC protocol error.
// For tool errors, use result.SetError(err) and return (result, nil).
func Bridge(srv *mcp.Server, reg *Registry, opts ...BridgeOption) {
	var cfg bridgeConfig
	for _, o := range opts {
		o(&cfg)
	}
	for _, t := range reg.ListTools() {
		registerDynamicTool(srv, reg, t, &cfg)
	}
}

func registerDynamicTool(srv *mcp.Server, reg *Registry, t *DynamicTool, cfg *bridgeConfig) {
	tool := &mcp.Tool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: json.RawMessage(mustMarshal(t.InputSchema)),
	}

	toolName := t.Name
	toolVersion := t.Version
	srv.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Policy check — before parsing arguments.
		if cfg.policy != nil {
			if err := cfg.policy(ctx, toolName); err != nil {
				var res mcp.CallToolResult
				res.SetError(err)
				return &res, nil
			}
		}

		var params map[string]any
		if req.Params.Arguments != nil {
			if err := json.Unmarshal(req.Params.Arguments, &params); err != nil {
				var res mcp.CallToolResult
				res.SetError(fmt.Errorf("%s: invalid arguments: %w", toolName, err))
				return &res, nil
			}
		}

		start := time.Now()
		result, execErr := reg.ExecuteTool(ctx, toolName, params)
		duration := time.Since(start)

		// Audit hook — always called, even on error.
		if cfg.audit != nil {
			cfg.audit(ctx, toolName, toolVersion, params, result, execErr, duration)
		}

		if execErr != nil {
			var res mcp.CallToolResult
			res.SetError(errors.New(fmt.Sprintf("%s: %v", toolName, execErr)))
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

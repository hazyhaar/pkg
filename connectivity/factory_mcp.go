package connectivity

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"

	"github.com/hazyhaar/pkg/mcpquic"
)

// mcpConfig is the per-route config parsed from the routes table JSON
// for MCP-over-QUIC transport.
type mcpConfig struct {
	ToolName    string `json:"tool_name"`
	InsecureTLS bool   `json:"insecure_tls"`
}

// MCPFactory creates Handlers that dispatch calls as MCP tool invocations
// over QUIC. The payload is unmarshalled as a JSON map of tool arguments.
// The endpoint is a QUIC address (e.g. "10.0.0.5:4433").
//
// The route config JSON must include "tool_name" to specify which MCP tool
// to call. Example config:
//
//	{"tool_name": "billing_process", "insecure_tls": true}
//
// Register it with:
//
//	router.RegisterTransport("mcp", connectivity.MCPFactory())
func MCPFactory() TransportFactory {
	return func(endpoint string, config json.RawMessage) (Handler, func(), error) {
		var cfg mcpConfig
		if len(config) > 0 {
			if err := json.Unmarshal(config, &cfg); err != nil {
				return nil, nil, fmt.Errorf("connectivity/mcp: parse config: %w", err)
			}
		}
		if cfg.ToolName == "" {
			return nil, nil, fmt.Errorf("connectivity/mcp: tool_name required in config")
		}

		var tlsCfg *tls.Config
		if cfg.InsecureTLS {
			tlsCfg = mcpquic.ClientTLSConfig(true)
		}

		client := mcpquic.NewClient(endpoint, tlsCfg)

		// Connect eagerly so we fail fast during Reload.
		if err := client.Connect(context.Background()); err != nil {
			return nil, nil, fmt.Errorf("connectivity/mcp: connect to %s: %w", endpoint, err)
		}

		handler := func(ctx context.Context, payload []byte) ([]byte, error) {
			var args map[string]any
			if len(payload) > 0 {
				if err := json.Unmarshal(payload, &args); err != nil {
					return nil, fmt.Errorf("connectivity/mcp: unmarshal args: %w", err)
				}
			}

			result, err := client.CallTool(ctx, cfg.ToolName, args)
			if err != nil {
				return nil, fmt.Errorf("connectivity/mcp: call %s: %w", cfg.ToolName, err)
			}

			resp, err := json.Marshal(result)
			if err != nil {
				return nil, fmt.Errorf("connectivity/mcp: marshal result: %w", err)
			}
			return resp, nil
		}

		closeFn := func() {
			client.Close()
		}

		return handler, closeFn, nil
	}
}

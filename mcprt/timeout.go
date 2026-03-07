// CLAUDE:SUMMARY Per-tool timeout configuration — BridgeOption that enables context deadlines from the timeout_ms column in mcp_tools_registry.
// CLAUDE:DEPENDS
// CLAUDE:EXPORTS WithTimeoutFromDB

package mcprt

// WithTimeoutFromDB enables per-tool timeouts read from the timeout_ms
// column in mcp_tools_registry. Each tool execution is wrapped in a
// context.WithTimeout derived from the stored value.
// A value of 0 in the database means no timeout is applied.
func WithTimeoutFromDB() BridgeOption {
	return func(c *bridgeConfig) { c.timeoutDB = true }
}

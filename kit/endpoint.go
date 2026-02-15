package kit

import "context"

// Endpoint is a transport-agnostic action function.
// Each action (create node, vote, search) is an Endpoint.
// HTTP handlers and MCP tools both dispatch to the same Endpoints.
type Endpoint func(ctx context.Context, request any) (response any, err error)

// Middleware wraps an Endpoint with cross-cutting concerns (audit, auth, tracing).
type Middleware func(Endpoint) Endpoint

// Chain composes middlewares so the first is outermost.
// Chain(a, b, c)(endpoint) == a(b(c(endpoint)))
func Chain(outer Middleware, others ...Middleware) Middleware {
	return func(next Endpoint) Endpoint {
		for i := len(others) - 1; i >= 0; i-- {
			next = others[i](next)
		}
		return outer(next)
	}
}

# chassis — unified HTTP + MCP server on one port

`chassis` multiplexes HTTP/1.1, HTTP/2, HTTP/3 and MCP-over-QUIC on a single
port using TLS ALPN negotiation.

```
TCP :4433 ─── TLS ────► HTTP/1.1 + HTTP/2

UDP :4433 ─── QUIC ──┬─ ALPN "h3"          ──► HTTP/3  (same handler)
                      └─ ALPN "mcp-quic-v1" ──► MCP JSON-RPC
```

HTTP/2 responses include an `Alt-Svc` header advertising HTTP/3 availability.

## Quick start

```go
srv, _ := chassis.New(chassis.Config{
    Addr:      ":4433",
    Handler:   mux,
    MCPServer: mcpServer,
    TLS:       chassis.DevelopmentTLSConfig(),
})
go srv.Start(ctx)
defer srv.Stop(context.Background())
```

## TLS modes

| Function | Use case |
|----------|----------|
| `DevelopmentTLSConfig()` | Self-signed ECDSA P-256, 1-year expiry |
| `ProductionTLSConfig(cert, key)` | Load certs from PEM files |

Both configurations include ALPN protocols `["h3", "mcp-quic-v1"]`.

## Exported API

| Symbol | Description |
|--------|-------------|
| `Server` | Unified TCP + QUIC server |
| `Config` | Addr, TLS, Handler, MCPServer, Logger |
| `New(cfg)` | Create server |
| `Start(ctx)` | Launch listeners (blocks) |
| `Stop(ctx)` | Graceful shutdown |
| `DevelopmentTLSConfig()` | Self-signed dev certificate |
| `ProductionTLSConfig(cert, key)` | Production certificate |
| `GenerateSelfSignedCert()` | ECDSA P-256 certificate |

# mcpquic — MCP transport over QUIC

`mcpquic` implements the Model Context Protocol (MCP) JSON-RPC transport over
QUIC with TLS 1.3, ALPN negotiation, and magic-byte framing.

```
Client                                     Server (chassis)
  │                                           │
  │── QUIC dial (ALPN "mcp-quic-v1") ───────►│
  │── magic bytes "MCP1" ──────────────────►│
  │◄──────── MCP initialize handshake ──────│
  │── CallTool / ListTools ────────────────►│
  │◄──────── JSON-RPC response ─────────────│
```

## Quick start

### Client

```go
client, _ := mcpquic.NewClient("localhost:4433", mcpquic.ClientTLSConfig(true))
defer client.Close()

client.Connect(ctx)
tools, _ := client.ListTools(ctx)
result, _ := client.CallTool(ctx, "search", map[string]any{"q": "hello"})
```

### Server (via chassis)

```go
handler := mcpquic.NewHandler(mcpServer,
    mcpquic.WithHandlerIDGenerator(idgen.NanoID(8)),
)
// chassis routes ALPN "mcp-quic-v1" to handler.ServeConn
```

### Standalone listener

```go
listener, _ := mcpquic.NewListener(":4433", tlsCfg, mcpServer, logger)
go listener.Serve(ctx)
```

## Protocol details

- **ALPN**: `mcp-quic-v1`
- **Magic bytes**: `MCP1` (4 bytes, defense-in-depth against ALPN confusion)
- **Max message**: 10 MiB
- **Idle timeout**: 5 min
- **Keep-alive**: 30 s

## Exported API

| Symbol | Description |
|--------|-------------|
| `Client` | MCP client over QUIC |
| `NewClient(addr, tlsCfg)` | Create client |
| `Handler` | Accepts MCP-over-QUIC connections |
| `NewHandler(srv, opts...)` | Create handler for chassis ALPN routing |
| `Listener` | Standalone QUIC listener |
| `ALPNProtocolMCP` | `"mcp-quic-v1"` |
| `ServerTLSConfig`, `ClientTLSConfig`, `SelfSignedTLSConfig` | TLS helpers |

╔══════════════════════════════════════════════════════════════════════════════╗
║  mcpquic — MCP-over-QUIC transport (client + server + listener)             ║
║            ALPN "mcp-quic-v1", magic bytes "MCP1", TLS 1.3 mandatory       ║
╠══════════════════════════════════════════════════════════════════════════════╣
║                                                                             ║
║  CONNECTION FLOW (Client -> Server)                                         ║
║  ──────────────────────────────────                                         ║
║                                                                             ║
║  ┌─────────────┐        QUIC + TLS 1.3         ┌──────────────────┐        ║
║  │   Client     │ ─── ALPN "mcp-quic-v1" ────→ │  Handler/        │        ║
║  │              │                               │  Listener        │        ║
║  │ 1. DialAddr  │                               │                  │        ║
║  │ 2. Check ALPN│◄─────────────────────────────→│ 1. Accept conn   │        ║
║  │ 3. OpenStream│                               │ 2. Verify ALPN   │        ║
║  │ 4. Send MCP1 │ ───── "MCP1" (4 bytes) ────→ │ 3. Accept stream │        ║
║  │ 5. MCP init  │                               │ 4. ValidateMagic │        ║
║  │    handshake │◄── JSON-RPC initialize ─────→ │ 5. MCP session   │        ║
║  │ 6. Session   │◄── JSON-RPC messages ──────→ │ 6. Wait()        │        ║
║  └─────────────┘                               └──────────────────┘        ║
║                                                                             ║
║  SECURITY LAYERS                                                            ║
║  ───────────────                                                            ║
║                                                                             ║
║  Layer 1: TLS 1.3 minimum (never TLS 1.2)                                  ║
║  Layer 2: ALPN "mcp-quic-v1" — reject any other protocol                   ║
║  Layer 3: Magic bytes "MCP1" — defense-in-depth vs ALPN confusion          ║
║  Layer 4: 0-RTT disabled (Allow0RTT: false) — no replay attacks            ║
║                                                                             ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  TWO SERVER MODES                                                           ║
║  ────────────────                                                           ║
║                                                                             ║
║  1. Handler (per-connection, used by chassis with ALPN demuxing)            ║
║     ┌──────────────────────────────────────────────┐                        ║
║     │ chassis receives QUIC conn, checks ALPN      │                        ║
║     │ if "mcp-quic-v1" -> handler.ServeConn(conn)  │                        ║
║     │ if "h3"          -> HTTP/3 handler            │                        ║
║     └──────────────────────────────────────────────┘                        ║
║                                                                             ║
║  2. Listener (standalone accept loop, own UDP socket)                       ║
║     ┌──────────────────────────────────────────────┐                        ║
║     │ ListenAddr -> Accept loop -> go ServeConn    │                        ║
║     │ For standalone MCP servers without chassis   │                        ║
║     └──────────────────────────────────────────────┘                        ║
║                                                                             ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  CONTEXT ENRICHMENT (via kit)                                               ║
║  ────────────────────────────                                               ║
║                                                                             ║
║  Handler.ServeConn sets on every session:                                   ║
║    kit.WithTransport(ctx, "mcp_quic")                                      ║
║    kit.WithSessionID(ctx, "quic_<nanoid8>")                                ║
║    kit.WithRemoteAddr(ctx, conn.RemoteAddr())                              ║
║                                                                             ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED TYPES                                                             ║
║  ──────────────                                                             ║
║                                                                             ║
║  Client {                                                                   ║
║      addr string; tlsCfg *tls.Config                                       ║
║      conn *quic.Conn; stream *quic.Stream                                  ║
║      session *mcp.ClientSession                                            ║
║  }                                                                          ║
║                                                                             ║
║  Handler {                                                                  ║
║      mcpServer *mcp.Server; logger *slog.Logger                            ║
║      newID idgen.Generator                                                  ║
║  }                                                                          ║
║                                                                             ║
║  Listener {                                                                 ║
║      listener *quic.Listener; handler *Handler                             ║
║      mcpServer *mcp.Server; logger *slog.Logger                            ║
║  }                                                                          ║
║                                                                             ║
║  HandlerOption func(*Handler)                                               ║
║  ConnectionError { RemoteAddr string; Code; Err error }                     ║
║                                                                             ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  KEY FUNCTIONS                                                              ║
║  ─────────────                                                              ║
║                                                                             ║
║  CLIENT                                                                     ║
║  NewClient(addr string, tlsCfg *tls.Config) *Client                        ║
║      If tlsCfg nil -> ClientTLSConfig(false) (verify certs).               ║
║  (*Client).Connect(ctx) error                                              ║
║      Dial + ALPN check + OpenStream + SendMagic + MCP handshake.           ║
║  (*Client).ListTools(ctx) (*mcp.ListToolsResult, error)                    ║
║  (*Client).CallTool(ctx, name, args) (*mcp.CallToolResult, error)          ║
║  (*Client).Ping(ctx) error                                                 ║
║  (*Client).Close() error                                                   ║
║                                                                             ║
║  SERVER                                                                     ║
║  NewHandler(mcpSrv, logger, ...HandlerOption) *Handler                     ║
║      Default session ID: NanoID(8) with "quic_" prefix.                    ║
║  (*Handler).ServeConn(ctx, *quic.Conn)                                     ║
║      Accept stream, validate magic, create session, wait.                  ║
║  WithHandlerIDGenerator(gen) HandlerOption                                 ║
║                                                                             ║
║  LISTENER (standalone)                                                      ║
║  NewListener(addr, tlsCfg, mcpSrv, logger, ...opts) (*Listener, error)     ║
║  (*Listener).Serve(ctx) error                                              ║
║  (*Listener).Close() error                                                 ║
║                                                                             ║
║  TLS CONFIG                                                                 ║
║  ServerTLSConfig(certFile, keyFile) (*tls.Config, error)                   ║
║  SelfSignedTLSConfig() (*tls.Config, error)                                ║
║      ECDSA P-256, 1 year, localhost/127.0.0.1/::1.                         ║
║  ClientTLSConfig(insecure bool) *tls.Config                                ║
║  H3TLSConfig(base *tls.Config) *tls.Config                                ║
║      Clone with ALPN set to "h3" for HTTP/3 serving.                       ║
║                                                                             ║
║  MAGIC BYTES                                                                ║
║  SendMagicBytes(w io.Writer) error         Write "MCP1"                    ║
║  ValidateMagicBytes(r io.Reader) error     Read + verify "MCP1"            ║
║                                                                             ║
║  QUIC CONFIG                                                                ║
║  ProductionQUICConfig() *quic.Config                                       ║
║      MaxStreamWindow=10MB, MaxConnWindow=50MB, Idle=5min, KA=30s           ║
║                                                                             ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  CONSTANTS                                                                  ║
║  ─────────                                                                  ║
║                                                                             ║
║  ALPNProtocolMCP          = "mcp-quic-v1"                                  ║
║  MagicBytesMCP            = "MCP1"                                         ║
║  MaxMessageSize           = 10 MB                                          ║
║  DefaultHandshakeTimeout  = 10s                                            ║
║  DefaultIdleTimeout       = 5 min                                          ║
║  DefaultKeepAlive         = 30s                                            ║
║                                                                             ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  ERROR CODES                                                                ║
║  ───────────                                                                ║
║                                                                             ║
║  Stream-level:                                                              ║
║    0x00 StreamErrorNoError                                                  ║
║    0x02 StreamErrorProtocolConfusion                                        ║
║    0x03 StreamErrorMessageTooLarge                                          ║
║                                                                             ║
║  Connection-level:                                                          ║
║    0x00 ConnErrorNoError                                                    ║
║    0x01 ConnErrorUnsupportedALPN                                            ║
║    0x03 ConnErrorProtocolViolation                                          ║
║                                                                             ║
║  Sentinel errors:                                                           ║
║    ErrInvalidMagicBytes  "expected MCP1"                                   ║
║    ErrUnsupportedALPN    "mcp-quic-v1 not selected"                        ║
║    ErrConnectionClosed   "QUIC connection closed"                          ║
║                                                                             ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  FILES                                                                      ║
║  ─────                                                                      ║
║                                                                             ║
║  client.go   Client: NewClient, Connect, ListTools, CallTool, Ping, Close  ║
║  server.go   Handler, Listener, ServeConn, Serve, quicServerTransport,     ║
║              sessionConn, streamWriteCloser                                ║
║  config.go   TLS configs, QUIC config, constants                           ║
║  magic.go    SendMagicBytes, ValidateMagicBytes                            ║
║  errors.go   Error codes (stream + connection), sentinel errors,           ║
║              ConnectionError type                                          ║
║                                                                             ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES                                                               ║
║  ────────────                                                               ║
║                                                                             ║
║  External:                                                                  ║
║    github.com/modelcontextprotocol/go-sdk/mcp                              ║
║    github.com/quic-go/quic-go                                              ║
║                                                                             ║
║  Internal (pkg/):                                                           ║
║    github.com/hazyhaar/pkg/idgen   (session ID generation)                 ║
║    github.com/hazyhaar/pkg/kit     (context enrichment: transport,         ║
║                                     sessionID, remoteAddr)                 ║
║                                                                             ║
║  Stdlib: context, crypto/tls, crypto/ecdsa, crypto/elliptic, crypto/rand,  ║
║          crypto/x509, io, log/slog, time, errors, fmt, bytes, net, math/big║
║                                                                             ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  INTERNAL TYPES (unexported)                                                ║
║  ───────────────────────────                                                ║
║                                                                             ║
║  quicServerTransport  implements mcp.Transport for server-side streams     ║
║  sessionConn          wraps mcp.Connection with custom SessionID()         ║
║  streamWriteCloser    adapts *quic.Stream to io.WriteCloser                ║
║                                                                             ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  DATABASE TABLES                                                            ║
║  ───────────────                                                            ║
║  None. Pure transport, no storage.                                          ║
║                                                                             ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  INVARIANTS                                                                 ║
║  ──────────                                                                 ║
║                                                                             ║
║  - ALPN must be "mcp-quic-v1" — any other value = immediate reject         ║
║  - Magic bytes "MCP1" sent by client right after stream open               ║
║  - TLS 1.3 minimum — never TLS 1.2                                         ║
║  - 0-RTT disabled — no replay attack surface                               ║
║  - Do NOT use Listener when chassis is active (chassis demuxes by ALPN)    ║
║  - Handler = per-connection (chassis mode); Listener = standalone loop     ║
║  - Client with nil tlsCfg defaults to secure (verify server cert)          ║
║  - SDK handles JSON-RPC loop — ServerSession.Wait() blocks until close     ║
║                                                                             ║
╚══════════════════════════════════════════════════════════════════════════════╝

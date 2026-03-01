╔══════════════════════════════════════════════════════════════════════════════╗
║  chassis — Unified server: HTTP/1.1+H2 (TCP) + HTTP/3+MCP (QUIC), 1 port  ║
╠══════════════════════════════════════════════════════════════════════════════╣
║                                                                            ║
║  FILE MAP                                                                  ║
║  ────────                                                                  ║
║  server.go   Server, Config, New, Start, Stop, altSvcMiddleware            ║
║  tls.go      GenerateSelfSignedCert, DevelopmentTLSConfig,                 ║
║              ProductionTLSConfig                                           ║
║                                                                            ║
║  NETWORK ARCHITECTURE (single port, dual transport)                        ║
║  ──────────────────────────────────────────────────                         ║
║                                                                            ║
║                         ┌──── :8080 (same addr) ────┐                      ║
║                         │                           │                      ║
║                     TCP │                       UDP │                      ║
║                         │                           │                      ║
║                         ▼                           ▼                      ║
║          ┌──────────────────────┐    ┌──────────────────────────┐          ║
║          │  TLS Listener        │    │  QUIC Listener            │          ║
║          │  NextProtos:         │    │  ALPN demux:              │          ║
║          │   ["h2","http/1.1"]  │    │                          │          ║
║          │                      │    │  "h3" ──────────┐        │          ║
║          │  ┌────────────────┐  │    │                 ▼        │          ║
║          │  │ http.Server    │  │    │  ┌──────────────────┐    │          ║
║          │  │ Serve(tcpLn)   │  │    │  │ http3.Server     │    │          ║
║          │  │                │  │    │  │ ServeQUICConn()  │    │          ║
║          │  │ handler with   │  │    │  │ same handler +   │    │          ║
║          │  │ Alt-Svc header │  │    │  │ Alt-Svc header   │    │          ║
║          │  └────────────────┘  │    │  └──────────────────┘    │          ║
║          └──────────────────────┘    │                          │          ║
║                                      │  "mcp-quic-v1" ─┐       │          ║
║                                      │                  ▼       │          ║
║                                      │  ┌──────────────────┐   │          ║
║                                      │  │ mcpquic.Handler  │   │          ║
║                                      │  │ ServeConn(conn)  │   │          ║
║                                      │  │ JSON-RPC over    │   │          ║
║                                      │  │ QUIC streams     │   │          ║
║                                      │  └──────────────────┘   │          ║
║                                      │                          │          ║
║                                      │  unknown ALPN ──→ close  │          ║
║                                      │  with error code 0x11    │          ║
║                                      └──────────────────────────┘          ║
║                                                                            ║
║  TLS CONFIGURATION                                                         ║
║  ─────────────────                                                         ║
║                                                                            ║
║  ┌────────────────────────────────────────────────────────────┐            ║
║  │ Config.TLS != nil?  ──→ use directly                      │            ║
║  │       │ nil                                                │            ║
║  │       ▼                                                    │            ║
║  │ CertFile+KeyFile set?  ──→ ProductionTLSConfig()          │            ║
║  │       │ no                   loads X.509 keypair from files│            ║
║  │       ▼                      TLS 1.3 min                  │            ║
║  │ DevelopmentTLSConfig()       NextProtos: ["h3","mcp-..."] │            ║
║  │   GenerateSelfSignedCert()                                │            ║
║  │   ECDSA P-256                                             │            ║
║  │   CN=localhost                                            │            ║
║  │   SANs: localhost, 127.0.0.1, ::1                         │            ║
║  │   Valid: 1 year                                           │            ║
║  │   TLS 1.3 min                                             │            ║
║  │   NextProtos: ["h3","mcp-quic-v1"]                        │            ║
║  └────────────────────────────────────────────────────────────┘            ║
║                                                                            ║
║  START/STOP LIFECYCLE                                                      ║
║  ────────────────────                                                      ║
║                                                                            ║
║  New(Config) ──→ *Server                                                   ║
║       │                                                                    ║
║  Start(ctx) ──→ launches:                                                  ║
║       │  1. TCP listener goroutine (TLS, HTTP/1.1+H2)                      ║
║       │  2. QUIC accept loop goroutine (ALPN demux)                        ║
║       │  blocks until ctx.Done() or fatal error                            ║
║       │                                                                    ║
║  Stop(ctx) ──→ graceful shutdown:                                          ║
║       1. tcpServer.Shutdown(ctx)  -- drain HTTP conns                      ║
║       2. quicLn.Close()           -- stop accepting QUIC                   ║
║       3. h3Server.Close()         -- close HTTP/3                          ║
║                                                                            ║
║  Alt-Svc HEADER                                                            ║
║  ──────────────                                                            ║
║  All HTTP responses include:                                               ║
║    Alt-Svc: h3=":PORT"; ma=86400                                           ║
║  Enables HTTP/2 clients to discover and upgrade to HTTP/3                   ║
║                                                                            ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  QUIC CONFIGURATION                                                        ║
║  ──────────────────                                                        ║
║  MaxStreamReceiveWindow:     10 MiB                                        ║
║  MaxConnectionReceiveWindow: 50 MiB                                        ║
║  MaxIdleTimeout:             mcpquic.DefaultIdleTimeout                    ║
║  KeepAlivePeriod:            mcpquic.DefaultKeepAlive                      ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED TYPES                                                            ║
║  ──────────────                                                            ║
║  Server  struct {                                                          ║
║      addr        string                                                    ║
║      logger      *slog.Logger                                              ║
║      tlsCfg      *tls.Config                                               ║
║      httpHandler http.Handler     -- user-provided mux                     ║
║      mcpServer   *mcp.Server      -- nil = MCP disabled                    ║
║      mcpHandler  *mcpquic.Handler -- created from mcpServer                ║
║      h3Server    *http3.Server                                             ║
║      tcpServer   *http.Server                                              ║
║      quicLn      *quic.Listener                                            ║
║  }                                                                         ║
║                                                                            ║
║  Config  struct {                                                          ║
║      Addr      string             -- ":8080" TCP+UDP same port             ║
║      TLS       *tls.Config        -- nil = auto-detect                     ║
║      CertFile  string             -- production cert path                  ║
║      KeyFile   string             -- production key path                   ║
║      Handler   http.Handler       -- HTTP mux (API + static)              ║
║      MCPServer *mcp.Server        -- nil = MCP disabled                    ║
║      Logger    *slog.Logger                                                ║
║      MCPHandlerOpts []mcpquic.HandlerOption                                ║
║  }                                                                         ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED FUNCTIONS                                                        ║
║  ──────────────────                                                        ║
║  New(cfg Config) (*Server, error)                                          ║
║  (s *Server) Start(ctx context.Context) error                              ║
║      -- blocks until ctx cancelled or fatal error                          ║
║  (s *Server) Stop(ctx context.Context) error                               ║
║      -- graceful shutdown of TCP + QUIC + HTTP/3                           ║
║                                                                            ║
║  GenerateSelfSignedCert() (tls.Certificate, error)                         ║
║      -- ECDSA P-256, localhost, 1 year, dev only                           ║
║  DevelopmentTLSConfig() (*tls.Config, error)                               ║
║      -- self-signed, TLS 1.3, NextProtos=[h3, mcp-quic-v1]                ║
║  ProductionTLSConfig(certFile, keyFile string) (*tls.Config, error)        ║
║      -- file-based, TLS 1.3, NextProtos=[h3, mcp-quic-v1]                 ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES (internal)                                                   ║
║  ────────────────────────                                                  ║
║  github.com/hazyhaar/pkg/mcpquic                                           ║
║      -- NewHandler, ServeConn, ALPNProtocolMCP ("mcp-quic-v1"),            ║
║         DefaultIdleTimeout, DefaultKeepAlive, HandlerOption                ║
║                                                                            ║
║  DEPENDENCIES (external)                                                   ║
║  ────────────────────────                                                  ║
║  github.com/quic-go/quic-go          -- QUIC listener, connection          ║
║  github.com/quic-go/quic-go/http3    -- HTTP/3 server                      ║
║  github.com/modelcontextprotocol/go-sdk/mcp  -- MCP server type            ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  ALPN PROTOCOL TABLE                                                       ║
║  ───────────────────                                                       ║
║  Protocol        │ Transport │ Handler                                     ║
║  ────────────────┼───────────┼────────────────────────                     ║
║  "h2"            │ TCP/TLS   │ http.Server (standard Go)                   ║
║  "http/1.1"      │ TCP/TLS   │ http.Server (standard Go)                   ║
║  "h3"            │ UDP/QUIC  │ http3.Server.ServeQUICConn()                ║
║  "mcp-quic-v1"   │ UDP/QUIC  │ mcpquic.Handler.ServeConn()                ║
║  (other)         │ UDP/QUIC  │ closed with error 0x11                      ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  INVARIANTS                                                                ║
║  ──────────                                                                ║
║  - TCP and UDP listen on the SAME port (addr shared)                       ║
║  - QUIC demux is purely ALPN-based (no path inspection)                    ║
║  - TLS 1.3 minimum — no fallback to TLS 1.2                               ║
║  - MCPServer=nil → QUIC MCP connections rejected (error code 0x10)         ║
║  - Alt-Svc header injected on ALL HTTP responses automatically             ║
║  - Self-signed cert is dev ONLY — production requires CertFile+KeyFile     ║
║  - Stop() must be called explicitly — listeners do not auto-close          ║
║  - TCP NextProtos overridden to ["h2","http/1.1"] (no MCP on TCP)          ║
╚══════════════════════════════════════════════════════════════════════════════╝

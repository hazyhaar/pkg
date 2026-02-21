// Package chassis provides a unified server exposing HTTP and MCP on the same port.
//
// Two listeners on the same port:
//   - TCP → HTTP/1.1 + HTTP/2 (TLS) — curl-friendly REST API + static files
//   - UDP → QUIC with ALPN demux:
//     "h3"           → HTTP/3 (same handler as TCP)
//     "mcp-quic-v1"  → MCP JSON-RPC over QUIC stream
//
// The HTTP responses include an Alt-Svc header advertising HTTP/3,
// so HTTP/2 clients that support it can upgrade transparently.
//
// In development mode, a self-signed ECDSA P-256 cert is generated automatically.
// In production, supply cert/key files via config.
package chassis

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"

	"github.com/hazyhaar/pkg/mcpquic"
)

// Server is the unified chassis. It runs:
// - HTTP/1.1+HTTP/2 on TCP (curl-friendly, API first)
// - HTTP/3 + MCP-over-QUIC on UDP (same port, ALPN demux)
type Server struct {
	addr        string
	logger      *slog.Logger
	tlsCfg      *tls.Config
	httpHandler http.Handler
	mcpServer   *mcp.Server
	mcpHandler  *mcpquic.Handler
	h3Server    *http3.Server
	tcpServer   *http.Server
	quicLn      *quic.Listener
	mu          sync.Mutex
}

// Config holds configuration for the chassis server.
type Config struct {
	Addr      string       // Listen address (e.g. ":8080") — TCP + UDP same port
	TLS       *tls.Config  // nil = auto-generate self-signed
	CertFile  string       // production cert path
	KeyFile   string       // production key path
	Handler   http.Handler // HTTP handler (mux with API + static)
	MCPServer *mcp.Server  // MCP server (nil = MCP disabled)
	Logger    *slog.Logger

	// MCPHandlerOpts are passed to mcpquic.NewHandler.
	MCPHandlerOpts []mcpquic.HandlerOption
}

func New(cfg Config) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	tlsCfg := cfg.TLS
	if tlsCfg == nil {
		if cfg.CertFile != "" && cfg.KeyFile != "" {
			var err error
			tlsCfg, err = ProductionTLSConfig(cfg.CertFile, cfg.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("load TLS cert: %w", err)
			}
			cfg.Logger.Info("TLS: production certs loaded")
		} else {
			var err error
			tlsCfg, err = DevelopmentTLSConfig()
			if err != nil {
				return nil, fmt.Errorf("generate dev TLS: %w", err)
			}
			cfg.Logger.Info("TLS: self-signed dev cert generated")
		}
	}

	s := &Server{
		addr:        cfg.Addr,
		logger:      cfg.Logger,
		tlsCfg:      tlsCfg,
		httpHandler: cfg.Handler,
		mcpServer:   cfg.MCPServer,
	}

	if cfg.MCPServer != nil {
		s.mcpHandler = mcpquic.NewHandler(cfg.MCPServer, cfg.Logger, cfg.MCPHandlerOpts...)
	}

	return s, nil
}

// altSvcMiddleware wraps an http.Handler and adds Alt-Svc header
// to advertise HTTP/3 availability on the same port.
func altSvcMiddleware(addr string, next http.Handler) http.Handler {
	_, port, _ := net.SplitHostPort(addr)
	if port == "" {
		port = "8080"
	}
	altSvc := fmt.Sprintf(`h3=":%s"; ma=86400`, port)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Alt-Svc", altSvc)
		next.ServeHTTP(w, r)
	})
}

// Start launches both TCP and UDP listeners.
// TCP serves HTTP/1.1+HTTP/2 (TLS). UDP serves QUIC (HTTP/3 + MCP).
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()

	handler := altSvcMiddleware(s.addr, s.httpHandler)

	// --- TCP: HTTP/1.1 + HTTP/2 (TLS) ---
	tcpTLS := s.tlsCfg.Clone()
	tcpTLS.NextProtos = []string{"h2", "http/1.1"}

	s.tcpServer = &http.Server{
		Addr:      s.addr,
		Handler:   handler,
		TLSConfig: tcpTLS,
	}

	// --- UDP: QUIC (HTTP/3 + MCP) ---
	qCfg := &quic.Config{
		MaxStreamReceiveWindow:     10 * 1024 * 1024,
		MaxConnectionReceiveWindow: 50 * 1024 * 1024,
		MaxIdleTimeout:             mcpquic.DefaultIdleTimeout,
		KeepAlivePeriod:            mcpquic.DefaultKeepAlive,
	}

	ln, err := quic.ListenAddr(s.addr, s.tlsCfg, qCfg)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("QUIC listen: %w", err)
	}
	s.quicLn = ln

	s.h3Server = &http3.Server{
		Handler: handler,
	}

	s.mu.Unlock()

	s.logger.Info("chassis started",
		"addr", s.addr,
		"tcp", "HTTP/1.1+HTTP/2 (TLS)",
		"udp", "QUIC (HTTP/3 + MCP)",
	)

	// Start TCP server in goroutine
	errCh := make(chan error, 2)
	go func() {
		tcpLn, err := tls.Listen("tcp", s.addr, tcpTLS)
		if err != nil {
			errCh <- fmt.Errorf("TCP listen: %w", err)
			return
		}
		s.logger.Info("TCP listener ready", "addr", s.addr, "proto", "HTTP/1.1+HTTP/2")
		if err := s.tcpServer.Serve(tcpLn); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("TCP: %w", err)
		}
	}()

	// QUIC accept loop: demux by ALPN
	go func() {
		for {
			conn, err := ln.Accept(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				errCh <- fmt.Errorf("QUIC accept: %w", err)
				return
			}

			alpn := conn.ConnectionState().TLS.NegotiatedProtocol
			switch alpn {
			case "h3":
				go func() {
					if err := s.h3Server.ServeQUICConn(conn); err != nil {
						s.logger.Debug("HTTP/3 conn done", "remote", conn.RemoteAddr(), "error", err)
					}
				}()
			case mcpquic.ALPNProtocolMCP:
				if s.mcpHandler != nil {
					go s.mcpHandler.ServeConn(ctx, conn)
				} else {
					conn.CloseWithError(quic.ApplicationErrorCode(0x10), "MCP not enabled")
				}
			default:
				s.logger.Warn("unknown ALPN, closing", "alpn", alpn, "remote", conn.RemoteAddr())
				conn.CloseWithError(quic.ApplicationErrorCode(0x11), "unsupported ALPN: "+alpn)
			}
		}
	}()

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

// Stop gracefully shuts down both TCP and QUIC listeners.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Info("chassis stopping")

	var firstErr error
	if s.tcpServer != nil {
		if err := s.tcpServer.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.quicLn != nil {
		if err := s.quicLn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.h3Server != nil {
		if err := s.h3Server.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	s.logger.Info("chassis stopped")
	return firstErr
}

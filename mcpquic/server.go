package mcpquic

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/quic-go/quic-go"

	"github.com/hazyhaar/pkg/idgen"
	"github.com/hazyhaar/pkg/kit"
)

// Handler handles individual MCP-over-QUIC connections without owning a listener.
// Used by the chassis for ALPN-based demuxing on a shared UDP socket.
//
// Migration note (feb 2026): uses official SDK (modelcontextprotocol/go-sdk).
// The SDK owns the JSON-RPC read/write loop via Transport/Connection interfaces.
// We implement quicServerTransport which wraps mcp.IOTransport over QUIC streams.
// sessionConn adds a custom SessionID to the underlying ioConn (which returns "").
// If sessions leak or hang, check that ServerSession.Wait() unblocks on QUIC
// stream closure and that DefaultIdleTimeout is propagated correctly.
type Handler struct {
	mcpServer *mcp.Server
	logger    *slog.Logger
	newID     idgen.Generator
}

// HandlerOption configures a Handler.
type HandlerOption func(*Handler)

// WithHandlerIDGenerator sets a custom ID generator for session IDs.
func WithHandlerIDGenerator(gen idgen.Generator) HandlerOption {
	return func(h *Handler) { h.newID = gen }
}

// NewHandler creates an MCP connection handler for use with chassis demuxing.
func NewHandler(mcpSrv *mcp.Server, logger *slog.Logger, opts ...HandlerOption) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	h := &Handler{
		mcpServer: mcpSrv,
		logger:    logger,
		newID:     idgen.NanoID(8),
	}
	for _, o := range opts {
		o(h)
	}
	return h
}

// ServeConn handles a single QUIC connection as an MCP session.
func (h *Handler) ServeConn(ctx context.Context, conn *quic.Conn) {
	remote := conn.RemoteAddr().String()
	h.logger.Info("MCP connection accepted", "remote", remote)

	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		h.logger.Error("MCP accept stream failed", "remote", remote, "error", err)
		conn.CloseWithError(ConnErrorProtocolViolation, "stream accept failed")
		return
	}

	if err := ValidateMagicBytes(stream); err != nil {
		h.logger.Error("MCP magic bytes invalid", "remote", remote, "error", err)
		stream.CancelWrite(StreamErrorProtocolConfusion)
		stream.CancelRead(StreamErrorProtocolConfusion)
		conn.CloseWithError(ConnErrorProtocolViolation, "invalid magic bytes")
		return
	}

	sessionID := "quic_" + h.newID()
	h.logger.Info("MCP session starting", "session", sessionID, "remote", remote)

	// Enrich context with session identity for policy and audit.
	ctx = kit.WithTransport(ctx, "mcp_quic")
	ctx = kit.WithSessionID(ctx, sessionID)
	ctx = kit.WithRemoteAddr(ctx, remote)
	transport := &quicServerTransport{
		stream:    stream,
		sessionID: sessionID,
	}

	// Connect the MCP server over this transport — the SDK handles the
	// full JSON-RPC read/write loop and session lifecycle internally.
	ss, err := h.mcpServer.Connect(ctx, transport, nil)
	if err != nil {
		h.logger.Error("MCP connect failed", "session", sessionID, "error", err)
		stream.Close()
		return
	}

	// Wait for the session to end (client disconnect or context cancellation).
	if err := ss.Wait(); err != nil {
		h.logger.Debug("MCP session ended", "session", sessionID, "error", err)
	}

	h.logger.Info("MCP session ended", "session", sessionID, "remote", remote)
}

// Listener accepts MCP-over-QUIC connections and dispatches to a shared MCP Server.
// For standalone use (without chassis). The chassis uses Handler directly.
type Listener struct {
	listener  *quic.Listener
	handler   *Handler
	mcpServer *mcp.Server
	logger    *slog.Logger
}

func NewListener(addr string, tlsCfg *tls.Config, mcpSrv *mcp.Server, logger *slog.Logger, opts ...HandlerOption) (*Listener, error) {
	if logger == nil {
		logger = slog.Default()
	}
	qCfg := ProductionQUICConfig()
	l, err := quic.ListenAddr(addr, tlsCfg, qCfg)
	if err != nil {
		return nil, err
	}
	logger.Info("MCP QUIC listener ready", "addr", addr)
	return &Listener{
		listener:  l,
		handler:   NewHandler(mcpSrv, logger, opts...),
		mcpServer: mcpSrv,
		logger:    logger,
	}, nil
}

func (l *Listener) Serve(ctx context.Context) error {
	for {
		conn, err := l.listener.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			l.logger.Error("QUIC accept error", "error", err)
			continue
		}

		alpn := conn.ConnectionState().TLS.NegotiatedProtocol
		if alpn != ALPNProtocolMCP {
			conn.CloseWithError(ConnErrorUnsupportedALPN, "unsupported ALPN: "+alpn)
			continue
		}

		go l.handler.ServeConn(ctx, conn)
	}
}

func (l *Listener) Close() error {
	return l.listener.Close()
}

// quicServerTransport implements mcp.Transport for server-side QUIC streams.
type quicServerTransport struct {
	stream    *quic.Stream
	sessionID string
}

func (t *quicServerTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	iot := &mcp.IOTransport{
		Reader: io.NopCloser(t.stream),
		Writer: streamWriteCloser{t.stream},
	}
	conn, err := iot.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &sessionConn{Connection: conn, id: t.sessionID}, nil
}

// sessionConn wraps an mcp.Connection to provide a custom session ID.
type sessionConn struct {
	mcp.Connection
	id string
}

func (c *sessionConn) SessionID() string { return c.id }

// streamWriteCloser adapts a *quic.Stream to io.WriteCloser.
type streamWriteCloser struct{ stream *quic.Stream }

func (w streamWriteCloser) Write(p []byte) (int, error) { return w.stream.Write(p) }
func (w streamWriteCloser) Close() error                { return w.stream.Close() }

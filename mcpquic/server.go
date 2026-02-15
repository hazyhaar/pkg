package mcpquic

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/quic-go/quic-go"

	"github.com/hazyhaar/pkg/idgen"
	"github.com/hazyhaar/pkg/kit"
)

// Handler handles individual MCP-over-QUIC connections without owning a listener.
// Used by the chassis for ALPN-based demuxing on a shared UDP socket.
type Handler struct {
	mcpServer *server.MCPServer
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
func NewHandler(mcpSrv *server.MCPServer, logger *slog.Logger, opts ...HandlerOption) *Handler {
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

	sess := newSession(sessionID, stream)
	if err := h.mcpServer.RegisterSession(ctx, sess); err != nil {
		h.logger.Error("session register failed", "session", sessionID, "error", err)
		stream.Close()
		return
	}
	defer h.mcpServer.UnregisterSession(ctx, sessionID)

	ctx = kit.WithTransport(ctx, "mcp_quic")
	ctx = h.mcpServer.WithContext(ctx, sess)

	go sess.writeNotifications(ctx, stream)

	reader := bufio.NewReader(stream)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err != io.EOF && ctx.Err() == nil {
				h.logger.Error("MCP read error", "session", sessionID, "error", err)
			}
			break
		}

		line = line[:len(line)-1]
		if len(line) == 0 {
			continue
		}

		response := h.mcpServer.HandleMessage(ctx, json.RawMessage(line))
		if response == nil {
			continue
		}

		data, err := json.Marshal(response)
		if err != nil {
			h.logger.Error("MCP marshal failed", "session", sessionID, "error", err)
			continue
		}

		data = append(data, '\n')
		if _, err := stream.Write(data); err != nil {
			h.logger.Error("MCP write error", "session", sessionID, "error", err)
			break
		}
	}

	h.logger.Info("MCP session ended", "session", sessionID, "remote", remote)
}

// Listener accepts MCP-over-QUIC connections and dispatches to a shared MCPServer.
// For standalone use (without chassis). The chassis uses Handler directly.
type Listener struct {
	listener  *quic.Listener
	handler   *Handler
	mcpServer *server.MCPServer
	logger    *slog.Logger
}

func NewListener(addr string, tlsCfg *tls.Config, mcpSrv *server.MCPServer, logger *slog.Logger, opts ...HandlerOption) (*Listener, error) {
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

// session implements server.ClientSession for a single QUIC connection.
type session struct {
	id            string
	notifications chan mcp.JSONRPCNotification
	initialized   atomic.Bool
	writer        io.Writer
	mu            sync.RWMutex
}

func newSession(id string, writer io.Writer) *session {
	return &session{
		id:            id,
		notifications: make(chan mcp.JSONRPCNotification, 100),
		writer:        writer,
	}
}

func (s *session) SessionID() string                                   { return s.id }
func (s *session) NotificationChannel() chan<- mcp.JSONRPCNotification { return s.notifications }
func (s *session) Initialize()                                         { s.initialized.Store(true) }
func (s *session) Initialized() bool                                   { return s.initialized.Load() }

func (s *session) writeNotifications(ctx context.Context, w io.Writer) {
	for {
		select {
		case notif := <-s.notifications:
			data, err := json.Marshal(notif)
			if err != nil {
				continue
			}
			data = append(data, '\n')
			s.mu.Lock()
			_, _ = w.Write(data)
			s.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}

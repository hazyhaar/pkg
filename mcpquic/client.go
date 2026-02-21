package mcpquic

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/quic-go/quic-go"
)

// Client connects to an MCP server over QUIC.
//
// Migration note (feb 2026): uses official SDK (modelcontextprotocol/go-sdk).
// Client.Connect now calls mcp.Client.Connect() which handles the full MCP
// initialize handshake internally (no more separate Start + Initialize).
// The returned ClientSession is used for all tool calls.
// ListTools/CallTool signatures use SDK types (*ListToolsResult, *CallToolResult)
// which marshal identically to the old mcp-go types for JSON consumers.
type Client struct {
	addr    string
	tlsCfg  *tls.Config
	conn    *quic.Conn
	stream  *quic.Stream
	session *mcp.ClientSession
}

func NewClient(addr string, tlsCfg *tls.Config) *Client {
	if tlsCfg == nil {
		tlsCfg = ClientTLSConfig(false) // secure by default: verify server cert
	}
	return &Client{addr: addr, tlsCfg: tlsCfg}
}

func (c *Client) Connect(ctx context.Context) error {
	qCfg := ProductionQUICConfig()
	conn, err := quic.DialAddr(ctx, c.addr, c.tlsCfg, qCfg)
	if err != nil {
		return fmt.Errorf("quic dial %s: %w", c.addr, err)
	}

	alpn := conn.ConnectionState().TLS.NegotiatedProtocol
	if alpn != ALPNProtocolMCP {
		conn.CloseWithError(ConnErrorUnsupportedALPN, "bad ALPN")
		return fmt.Errorf("%w: got %q", ErrUnsupportedALPN, alpn)
	}

	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		conn.CloseWithError(ConnErrorProtocolViolation, "stream open failed")
		return fmt.Errorf("open stream: %w", err)
	}

	if err := SendMagicBytes(stream); err != nil {
		stream.Close()
		conn.CloseWithError(ConnErrorProtocolViolation, "magic bytes failed")
		return err
	}

	c.conn = conn
	c.stream = stream

	// Wrap the QUIC stream as an MCP transport.
	transport := &mcp.IOTransport{
		Reader: io.NopCloser(stream),
		Writer: streamWriteCloser{stream},
	}

	mcpClient := mcp.NewClient(&mcp.Implementation{
		Name:    "horos-quic-client",
		Version: "1.0.0",
	}, nil)

	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Connect handles the full initialize handshake automatically.
	session, err := mcpClient.Connect(connectCtx, transport, nil)
	if err != nil {
		c.closeTransport()
		return fmt.Errorf("mcp connect: %w", err)
	}

	c.session = session
	return nil
}

func (c *Client) ListTools(ctx context.Context) (*mcp.ListToolsResult, error) {
	if c.session == nil {
		return nil, fmt.Errorf("client not connected")
	}
	return c.session.ListTools(ctx, nil)
}

func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	if c.session == nil {
		return nil, fmt.Errorf("client not connected")
	}
	return c.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
}

func (c *Client) Ping(ctx context.Context) error {
	if c.session == nil {
		return fmt.Errorf("client not connected")
	}
	return c.session.Ping(ctx, nil)
}

func (c *Client) Close() error {
	if c.session != nil {
		c.session.Close()
	}
	return c.closeTransport()
}

func (c *Client) closeTransport() error {
	if c.stream != nil {
		(*c.stream).Close()
	}
	if c.conn != nil {
		c.conn.CloseWithError(ConnErrorNoError, "client closing")
	}
	return nil
}

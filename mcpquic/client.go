package mcpquic

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/quic-go/quic-go"
)

// Client connects to an MCP server over QUIC.
type Client struct {
	addr      string
	tlsCfg    *tls.Config
	conn      *quic.Conn
	stream    *quic.Stream
	mcpClient *client.Client
}

func NewClient(addr string, tlsCfg *tls.Config) *Client {
	if tlsCfg == nil {
		tlsCfg = ClientTLSConfig(true) // dev default: insecure
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

	stdioTransport := transport.NewIO(stream, &writeCloser{stream}, nopReadCloser{})
	mcpClient := client.NewClient(stdioTransport)

	if err := mcpClient.Start(ctx); err != nil {
		c.closeTransport()
		return fmt.Errorf("mcp start: %w", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "horos-quic-client",
		Version: "1.0.0",
	}

	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := mcpClient.Initialize(initCtx, initReq); err != nil {
		c.closeTransport()
		return fmt.Errorf("mcp initialize: %w", err)
	}

	c.mcpClient = mcpClient
	return nil
}

func (c *Client) ListTools(ctx context.Context) (*mcp.ListToolsResult, error) {
	if c.mcpClient == nil {
		return nil, fmt.Errorf("client not connected")
	}
	return c.mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
}

func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	if c.mcpClient == nil {
		return nil, fmt.Errorf("client not connected")
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	return c.mcpClient.CallTool(ctx, req)
}

func (c *Client) Ping(ctx context.Context) error {
	if c.mcpClient == nil {
		return fmt.Errorf("client not connected")
	}
	return c.mcpClient.Ping(ctx)
}

func (c *Client) Close() error {
	if c.mcpClient != nil {
		c.mcpClient.Close()
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

func (c *Client) Underlying() *client.Client { return c.mcpClient }

type writeCloser struct{ stream *quic.Stream }

func (w *writeCloser) Write(p []byte) (int, error) { return (*w.stream).Write(p) }
func (w *writeCloser) Close() error                { return (*w.stream).Close() }

type nopReadCloser struct{}

func (nopReadCloser) Read([]byte) (int, error) { return 0, io.EOF }
func (nopReadCloser) Close() error             { return nil }

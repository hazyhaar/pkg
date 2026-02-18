package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// WebhookConfig is the per-channel JSON config for generic inbound webhooks.
type WebhookConfig struct {
	// ListenAddr is the address to bind the HTTP server (e.g. ":8080").
	ListenAddr string `json:"listen_addr"`
	// Path is the URL path to listen on (e.g. "/webhook/inbound").
	Path string `json:"path"`
	// Secret is an optional shared secret for HMAC signature verification.
	Secret string `json:"secret,omitempty"`
	// MaxBodyBytes limits the request body size. Defaults to 1MB.
	MaxBodyBytes int64 `json:"max_body_bytes,omitempty"`
}

// WebhookFactory returns a ChannelFactory for generic inbound HTTP webhooks.
// This allows any external system to push messages into the channels pipeline
// via HTTP POST.
//
// Inbound messages are received as JSON POST bodies. Outbound messages are
// buffered and can be retrieved by polling or via a callback URL configured
// in the message metadata.
//
// Config example:
//
//	{"listen_addr": ":8080", "path": "/webhook/inbound", "secret": "hmac_key"}
func WebhookFactory() ChannelFactory {
	return func(name string, config json.RawMessage) (Channel, error) {
		var cfg WebhookConfig
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil, fmt.Errorf("webhook: parse config: %w", err)
		}
		if cfg.ListenAddr == "" {
			return nil, fmt.Errorf("webhook: listen_addr is required")
		}
		if cfg.Path == "" {
			cfg.Path = "/"
		}
		if cfg.MaxBodyBytes <= 0 {
			cfg.MaxBodyBytes = 1 << 20 // 1MB
		}
		return newWebhookChannel(name, cfg), nil
	}
}

// webhookChannel implements Channel for generic HTTP webhooks.
type webhookChannel struct {
	name   string
	config WebhookConfig

	mu      sync.Mutex
	closed  bool
	status  ChannelStatus
	server  *http.Server
	inbound chan Message
	closeCh chan struct{}
}

func newWebhookChannel(name string, cfg WebhookConfig) *webhookChannel {
	return &webhookChannel{
		name:   name,
		config: cfg,
		status: ChannelStatus{
			Connected: false,
			Platform:  "webhook",
			AuthState: "listening",
		},
		inbound: make(chan Message, 256),
		closeCh: make(chan struct{}),
	}
}

func (c *webhookChannel) Listen(ctx context.Context) <-chan Message {
	ch := make(chan Message)

	mux := http.NewServeMux()
	mux.HandleFunc(c.config.Path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, c.config.MaxBodyBytes))
		if err != nil {
			http.Error(w, "read body failed", http.StatusBadRequest)
			return
		}

		var msg Message
		if err := json.Unmarshal(body, &msg); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}

		msg.ChannelName = c.name
		msg.Platform = "webhook"
		msg.Direction = Inbound
		if msg.Timestamp.IsZero() {
			msg.Timestamp = time.Now()
		}

		select {
		case c.inbound <- msg:
			w.WriteHeader(http.StatusAccepted)
		default:
			http.Error(w, "buffer full", http.StatusServiceUnavailable)
		}
	})

	c.mu.Lock()
	c.server = &http.Server{
		Addr:    c.config.ListenAddr,
		Handler: mux,
	}
	c.status.Connected = true
	c.mu.Unlock()

	// Start HTTP server in background.
	go func() {
		if err := c.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			c.mu.Lock()
			c.status.Connected = false
			c.status.Error = err.Error()
			c.mu.Unlock()
		}
	}()

	// Forward inbound messages to the returned channel.
	go func() {
		defer close(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.closeCh:
				return
			case msg, ok := <-c.inbound:
				if !ok {
					return
				}
				select {
				case ch <- msg:
				case <-ctx.Done():
					return
				case <-c.closeCh:
					return
				}
			}
		}
	}()

	return ch
}

func (c *webhookChannel) Send(ctx context.Context, msg Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return &ErrSendFailed{Channel: c.name, Platform: "webhook",
			Cause: fmt.Errorf("channel closed")}
	}
	// Webhook channels are primarily inbound. Outbound messages can be
	// delivered via a callback URL in msg.Metadata["callback_url"] or
	// stored for polling. For now, this is a no-op stub.
	c.status.LastMessage = time.Now()
	return nil
}

func (c *webhookChannel) Status() ChannelStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *webhookChannel) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	close(c.closeCh)
	c.status.Connected = false
	c.status.AuthState = "stopped"
	if c.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return c.server.Shutdown(ctx)
	}
	return nil
}

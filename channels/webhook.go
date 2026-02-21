package channels

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/hazyhaar/pkg/horosafe"
)

// WebhookConfig is the per-channel JSON config for generic inbound webhooks.
type WebhookConfig struct {
	// ListenAddr is the address to bind the HTTP server (e.g. ":8080").
	ListenAddr string `json:"listen_addr"`
	// Path is the URL path to listen on (e.g. "/webhook/inbound").
	Path string `json:"path"`
	// Secret is an optional shared secret for HMAC-SHA256 signature verification.
	// When set, inbound requests must include an X-Signature-256 header with
	// the hex-encoded HMAC-SHA256 of the request body.
	Secret string `json:"secret,omitempty"`
	// MaxBodyBytes limits the request body size. Defaults to 1MB.
	MaxBodyBytes int64 `json:"max_body_bytes,omitempty"`
}

// WebhookFactory returns a ChannelFactory for generic inbound HTTP webhooks.
// This allows any external system to push messages into the channels pipeline
// via HTTP POST.
//
// Inbound messages are received as JSON POST bodies. If the config includes a
// secret, the handler verifies the X-Signature-256 HMAC header before accepting.
//
// Outbound messages are POSTed as JSON to msg.Metadata["callback_url"] when
// present; otherwise they are silently dropped (the caller is responsible for
// including a callback URL if it expects responses).
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

	mu         sync.Mutex
	closed     bool
	status     ChannelStatus
	server     *http.Server
	inbound    chan Message
	closeCh    chan struct{}
	listenOnce sync.Once
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

// verifyHMAC checks the X-Signature-256 header against the body.
// Returns true if verification passes or no secret is configured.
func (c *webhookChannel) verifyHMAC(body []byte, signature string) bool {
	if c.config.Secret == "" {
		return true
	}
	if signature == "" {
		return false
	}
	// Strip optional "sha256=" prefix (GitHub-style).
	const prefix = "sha256="
	if len(signature) > len(prefix) && signature[:len(prefix)] == prefix {
		signature = signature[len(prefix):]
	}
	decoded, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(c.config.Secret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), decoded)
}

func (c *webhookChannel) Listen(ctx context.Context) <-chan Message {
	ch := make(chan Message)

	// Start the HTTP server at most once, even if Listen is called multiple times.
	c.listenOnce.Do(func() {
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

			// Verify HMAC signature if a secret is configured.
			if !c.verifyHMAC(body, r.Header.Get("X-Signature-256")) {
				http.Error(w, "invalid signature", http.StatusForbidden)
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
			Addr:              c.config.ListenAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    1 << 16, // 64 KiB
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
	})

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
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return &ErrSendFailed{Channel: c.name, Platform: "webhook",
			Cause: fmt.Errorf("channel closed")}
	}

	callbackURL := msg.Metadata["callback_url"]
	if callbackURL == "" {
		// No callback URL — nothing to do. The caller did not provide a
		// return path, so the response is silently dropped.
		return nil
	}

	// SSRF guard: reject callback URLs pointing to private/loopback addresses.
	if err := horosafe.ValidateURL(callbackURL); err != nil {
		return &ErrSendFailed{Channel: c.name, Platform: "webhook",
			Cause: fmt.Errorf("callback url: %w", err)}
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return &ErrSendFailed{Channel: c.name, Platform: "webhook",
			Cause: fmt.Errorf("marshal response: %w", err)}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(body))
	if err != nil {
		return &ErrSendFailed{Channel: c.name, Platform: "webhook",
			Cause: fmt.Errorf("build request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")

	// Sign the outbound payload if a secret is configured.
	if c.config.Secret != "" {
		mac := hmac.New(sha256.New, []byte(c.config.Secret))
		mac.Write(body)
		req.Header.Set("X-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return &ErrSendFailed{Channel: c.name, Platform: "webhook",
			Cause: fmt.Errorf("callback POST: %w", err)}
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		return &ErrSendFailed{Channel: c.name, Platform: "webhook",
			Cause: fmt.Errorf("callback returned %d", resp.StatusCode)}
	}

	c.mu.Lock()
	c.status.LastMessage = time.Now()
	c.mu.Unlock()
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

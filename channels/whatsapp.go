package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// WhatsAppConfig is the per-channel JSON config for WhatsApp connections.
type WhatsAppConfig struct {
	// DeviceName is the display name for the linked device.
	DeviceName string `json:"device_name"`
	// StorePath is the path to whatsmeow's SQLite session database.
	StorePath string `json:"store_path"`
}

// WhatsAppFactory returns a ChannelFactory for WhatsApp connections using
// whatsmeow (tulir). Whatsmeow handles multi-device pairing, QR code auth,
// automatic reconnection, and all message types.
//
// The factory is a stub: it creates a whatsAppChannel that implements the
// Channel interface. The actual whatsmeow integration (WA client, event
// handlers, media download) is wired up when the dependency is available.
//
// Config example:
//
//	{"device_name": "horos-ariege", "store_path": "/data/wa_session.db"}
func WhatsAppFactory() ChannelFactory {
	return func(name string, config json.RawMessage) (Channel, error) {
		var cfg WhatsAppConfig
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil, fmt.Errorf("whatsapp: parse config: %w", err)
		}
		if cfg.StorePath == "" {
			return nil, fmt.Errorf("whatsapp: store_path is required")
		}
		if cfg.DeviceName == "" {
			cfg.DeviceName = name
		}
		return newWhatsAppChannel(name, cfg), nil
	}
}

// whatsAppChannel implements Channel for WhatsApp via whatsmeow.
type whatsAppChannel struct {
	name   string
	config WhatsAppConfig

	mu      sync.Mutex
	closed  bool
	status  ChannelStatus
	closeCh chan struct{}
}

func newWhatsAppChannel(name string, cfg WhatsAppConfig) *whatsAppChannel {
	return &whatsAppChannel{
		name:   name,
		config: cfg,
		status: ChannelStatus{
			Connected: false,
			Platform:  "whatsapp",
			AuthState: "pending_qr",
		},
		closeCh: make(chan struct{}),
	}
}

func (c *whatsAppChannel) Listen(ctx context.Context) <-chan Message {
	ch := make(chan Message)
	go func() {
		defer close(ch)
		// TODO: Wire up whatsmeow event handler.
		// whatsmeow.Client.AddEventHandler produces events that are
		// converted to Message structs and sent on ch.
		// For now, block until context is cancelled or channel is closed.
		select {
		case <-ctx.Done():
		case <-c.closeCh:
		}
	}()
	return ch
}

func (c *whatsAppChannel) Send(ctx context.Context, msg Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return &ErrSendFailed{Channel: c.name, Platform: "whatsapp",
			Cause: fmt.Errorf("channel closed")}
	}
	// TODO: Wire up whatsmeow.Client.SendMessage.
	// Convert Message to whatsmeow proto and send.
	c.status.LastMessage = time.Now()
	return nil
}

func (c *whatsAppChannel) Status() ChannelStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *whatsAppChannel) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	close(c.closeCh)
	c.status.Connected = false
	c.status.AuthState = "disconnected"
	// TODO: Call whatsmeow.Client.Disconnect().
	return nil
}

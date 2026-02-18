package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// TelegramConfig is the per-channel JSON config for Telegram connections.
type TelegramConfig struct {
	// BotToken is the Telegram bot API token (from @BotFather).
	// For security, prefer passing via environment variable and referencing
	// the env var name here.
	BotToken string `json:"bot_token"`
	// UseMTProto enables the full MTProto client (gotd/td) instead of the
	// simpler bot API. Required for user accounts, optional for bots.
	UseMTProto bool `json:"use_mtproto,omitempty"`
	// WebhookURL, if set, uses webhook mode instead of long-polling.
	// Must be a publicly reachable HTTPS URL.
	WebhookURL string `json:"webhook_url,omitempty"`
}

// TelegramFactory returns a ChannelFactory for Telegram connections.
//
// By default, uses the bot API with long-polling (go-telegram-bot-api or
// similar). Set use_mtproto=true in config for the full MTProto client
// (gotd/td), which supports user accounts and more message types.
//
// Config example (bot API):
//
//	{"bot_token": "123456:ABC-DEF"}
//
// Config example (MTProto):
//
//	{"bot_token": "123456:ABC-DEF", "use_mtproto": true}
func TelegramFactory() ChannelFactory {
	return func(name string, config json.RawMessage) (Channel, error) {
		var cfg TelegramConfig
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil, fmt.Errorf("telegram: parse config: %w", err)
		}
		if cfg.BotToken == "" {
			return nil, fmt.Errorf("telegram: bot_token is required")
		}
		return newTelegramChannel(name, cfg), nil
	}
}

// telegramChannel implements Channel for Telegram.
type telegramChannel struct {
	name   string
	config TelegramConfig

	mu      sync.Mutex
	closed  bool
	status  ChannelStatus
	closeCh chan struct{}
}

func newTelegramChannel(name string, cfg TelegramConfig) *telegramChannel {
	return &telegramChannel{
		name:   name,
		config: cfg,
		status: ChannelStatus{
			Connected: false,
			Platform:  "telegram",
			AuthState: "token_valid",
		},
		closeCh: make(chan struct{}),
	}
}

func (c *telegramChannel) Listen(ctx context.Context) <-chan Message {
	ch := make(chan Message)
	go func() {
		defer close(ch)
		// TODO: Wire up Telegram bot API long-polling or MTProto event loop.
		// For bot API: call getUpdates in a loop, convert to Message.
		// For MTProto: use gotd/td client dispatcher.
		select {
		case <-ctx.Done():
		case <-c.closeCh:
		}
	}()
	return ch
}

func (c *telegramChannel) Send(ctx context.Context, msg Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return &ErrSendFailed{Channel: c.name, Platform: "telegram",
			Cause: fmt.Errorf("channel closed")}
	}
	// TODO: Wire up Telegram sendMessage / sendPhoto / etc.
	c.status.LastMessage = time.Now()
	return nil
}

func (c *telegramChannel) Status() ChannelStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *telegramChannel) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	close(c.closeCh)
	c.status.Connected = false
	c.status.AuthState = "disconnected"
	// TODO: Stop polling / disconnect MTProto session.
	return nil
}

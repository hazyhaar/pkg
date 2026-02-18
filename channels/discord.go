package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// DiscordConfig is the per-channel JSON config for Discord connections.
type DiscordConfig struct {
	// BotToken is the Discord bot token (from Developer Portal).
	BotToken string `json:"bot_token"`
	// GuildID restricts the bot to a specific guild. If empty, the bot
	// listens on all guilds it has been invited to.
	GuildID string `json:"guild_id,omitempty"`
	// ChannelIDs restricts listening to specific channel IDs.
	// If empty, all channels the bot can see are monitored.
	ChannelIDs []string `json:"channel_ids,omitempty"`
	// Intents specifies the gateway intents. Defaults to MessageContent +
	// GuildMessages + DirectMessages if empty.
	Intents int `json:"intents,omitempty"`
}

// DiscordFactory returns a ChannelFactory for Discord connections using
// discordgo (bwmarrin). Discordgo handles the gateway WebSocket, REST API,
// slash commands, and voice connections.
//
// Config example:
//
//	{"bot_token": "Bot MTk...", "guild_id": "123456789"}
func DiscordFactory() ChannelFactory {
	return func(name string, config json.RawMessage) (Channel, error) {
		var cfg DiscordConfig
		if err := json.Unmarshal(config, &cfg); err != nil {
			return nil, fmt.Errorf("discord: parse config: %w", err)
		}
		if cfg.BotToken == "" {
			return nil, fmt.Errorf("discord: bot_token is required")
		}
		return newDiscordChannel(name, cfg), nil
	}
}

// discordChannel implements Channel for Discord via discordgo.
type discordChannel struct {
	name   string
	config DiscordConfig

	mu      sync.Mutex
	closed  bool
	status  ChannelStatus
	closeCh chan struct{}
}

func newDiscordChannel(name string, cfg DiscordConfig) *discordChannel {
	return &discordChannel{
		name:   name,
		config: cfg,
		status: ChannelStatus{
			Connected: false,
			Platform:  "discord",
			AuthState: "token_valid",
		},
		closeCh: make(chan struct{}),
	}
}

func (c *discordChannel) Listen(ctx context.Context) <-chan Message {
	ch := make(chan Message)
	go func() {
		defer close(ch)
		// TODO: Wire up discordgo session.
		// Open gateway WebSocket, register MessageCreate handler,
		// convert *discordgo.MessageCreate to Message and send on ch.
		// Filter by GuildID and ChannelIDs if configured.
		select {
		case <-ctx.Done():
		case <-c.closeCh:
		}
	}()
	return ch
}

func (c *discordChannel) Send(ctx context.Context, msg Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return &ErrSendFailed{Channel: c.name, Platform: "discord",
			Cause: fmt.Errorf("channel closed")}
	}
	// TODO: Wire up discordgo.Session.ChannelMessageSend.
	// Handle embeds, attachments, and reply threading.
	c.status.LastMessage = time.Now()
	return nil
}

func (c *discordChannel) Status() ChannelStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *discordChannel) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	close(c.closeCh)
	c.status.Connected = false
	c.status.AuthState = "disconnected"
	// TODO: Call discordgo.Session.Close().
	return nil
}

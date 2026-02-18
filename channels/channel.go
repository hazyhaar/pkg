// Package channels provides bidirectional messaging connectors for platforms
// like WhatsApp, Telegram, Discord, and generic webhooks.
//
// While connectivity handles synchronous request-response routing (bytes in,
// bytes out), channels handles long-lived, event-driven connections where
// messages arrive unprompted (inbound) and responses are pushed back (outbound).
//
// The two packages are complementary: channels uses connectivity.Router for
// outbound processing (LLM calls, tool invocations) while managing the
// platform-specific connection lifecycle (authentication, reconnection,
// keepalive) that doesn't fit the request-response model.
//
//	d := channels.NewDispatcher(router, handler, channels.WithLogger(logger))
//	d.RegisterPlatform("whatsapp", channels.WhatsAppFactory())
//	d.RegisterPlatform("telegram", channels.TelegramFactory())
//	go d.Watch(ctx, db, 200*time.Millisecond)
//
// The channels table in SQLite decides which connectors are active. Change it
// at runtime and the Dispatcher picks up the new config — zero downtime.
package channels

import (
	"context"
	"encoding/json"
	"time"
)

// Direction indicates whether a message is inbound (received from a user)
// or outbound (sent by the system).
type Direction int

const (
	Inbound  Direction = iota // Message received from a platform user.
	Outbound                  // Message sent to a platform user.
)

// String returns "inbound" or "outbound".
func (d Direction) String() string {
	if d == Inbound {
		return "inbound"
	}
	return "outbound"
}

// Message is a platform-normalized inbound or outbound message.
// All platform-specific details are stripped; platform-specific metadata
// can be carried in the Metadata map.
type Message struct {
	ID          string            `json:"id"`
	ChannelName string            `json:"channel"`            // e.g. "whatsapp_main", "tg_support"
	Platform    string            `json:"platform"`           // "whatsapp", "telegram", "discord", "webhook"
	Direction   Direction         `json:"direction"`          // Inbound or Outbound
	SenderID    string            `json:"sender_id"`          // platform-specific user ID
	RecipientID string            `json:"recipient_id"`       // platform-specific recipient ID
	Text        string            `json:"text"`               // message body
	ReplyTo     string            `json:"reply_to,omitempty"` // ID of message being replied to
	Attachments []Attachment      `json:"attachments,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"` // platform-specific extras
	Timestamp   time.Time         `json:"timestamp"`
}

// Attachment is a media file attached to a message.
type Attachment struct {
	Type     string `json:"type"`               // "image", "audio", "video", "document"
	URL      string `json:"url"`                // download URL or local path
	MimeType string `json:"mime_type"`          // e.g. "image/jpeg"
	Caption  string `json:"caption,omitempty"`  // optional caption
	Filename string `json:"filename,omitempty"` // original filename
}

// ChannelStatus describes the current state of a channel connection.
type ChannelStatus struct {
	Connected   bool      `json:"connected"`
	Platform    string    `json:"platform"`
	AuthState   string    `json:"auth_state"` // "paired", "pending_qr", "token_valid", etc.
	LastMessage time.Time `json:"last_message"`
	Error       string    `json:"error,omitempty"`
}

// Channel is a bidirectional connection to a messaging platform.
// Listen returns a channel that emits inbound messages; the channel is closed
// when the context is cancelled or the connection is lost.
// Send pushes an outbound message to the platform.
type Channel interface {
	// Listen returns a read-only channel of inbound messages.
	// The returned channel is closed when ctx is cancelled or Close is called.
	Listen(ctx context.Context) <-chan Message

	// Send pushes an outbound message to the platform.
	Send(ctx context.Context, msg Message) error

	// Status returns the current connection status.
	Status() ChannelStatus

	// Close shuts down the connection and releases resources.
	// After Close, the channel returned by Listen will be closed.
	Close() error
}

// ChannelFactory creates a Channel from a name and JSON config.
// Analogous to connectivity.TransportFactory but for bidirectional connections.
// The name is the channel's identifier in the channels table (e.g. "wa_principal").
// The config is the per-channel JSON from the config column.
type ChannelFactory func(name string, config json.RawMessage) (Channel, error)

// InboundHandler processes an inbound message and returns zero or more outbound
// response messages. This is the integration point where anonymization, LLM
// processing, and de-anonymization happen.
//
// The handler may return nil to indicate no response should be sent.
type InboundHandler func(ctx context.Context, msg Message) ([]Message, error)

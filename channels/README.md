# channels — bidirectional messaging with hot-reload

`channels` manages multiple messaging platform connectors (webhook, Discord,
Telegram, WhatsApp) with configuration stored in SQLite and hot-reloaded via
`PRAGMA data_version`.

```
                           ┌─────────────┐
  Telegram  ◄────────────►│             │
  Discord   ◄────────────►│  Dispatcher  │◄──── InboundHandler
  Webhook   ◄────────────►│             │       (your logic)
  WhatsApp  ◄────────────►│             │
                           └──────┬──────┘
                                  │ watch
                           ┌──────▼──────┐
                           │   channels   │  SQLite table
                           │   (config)   │
                           └─────────────┘
```

## Quick start

```go
dispatcher := channels.NewDispatcher(handler,
    channels.WithMaxConcurrent(10),
)
dispatcher.RegisterPlatform("webhook", channels.WebhookFactory())

channels.Init(db)
go channels.Watch(ctx, db, 2*time.Second) // hot-reload

dispatcher.Send(ctx, channels.Message{ChannelName: "alerts", Text: "hello"})
```

## Schema

```sql
CREATE TABLE channels (
    name       TEXT PRIMARY KEY,
    platform   TEXT NOT NULL,  -- webhook, telegram, discord, whatsapp, signal, matrix
    enabled    INTEGER NOT NULL DEFAULT 1,
    config     TEXT,           -- JSON, platform-specific
    auth_state TEXT,           -- JSON, managed by platform SDK
    updated_at INTEGER
);
```

## Hot-reload behavior

The watcher polls `PRAGMA data_version`. On change the dispatcher:
- **Starts** new channels.
- **Stops** removed or disabled channels.
- **Restarts** channels whose config fingerprint changed (auth_state changes are
  ignored since they are managed by platform SDKs).

## Webhook channel

- Inbound: starts an HTTP server, validates optional HMAC-SHA256 signature.
- Outbound: POSTs JSON to `msg.Metadata["callback_url"]` with SSRF guard.

## Exported API

| Symbol | Description |
|--------|-------------|
| `Channel` | Interface: Listen, Send, Status, Close |
| `Dispatcher` | Manages active channels with hot-reload |
| `Message` | Platform-normalized message |
| `ChannelFactory` | `func(name, config) (Channel, error)` |
| `InboundHandler` | `func(ctx, Message) ([]Message, error)` |
| `WebhookFactory()` | Factory for HTTP webhook channels |
| `WithMaxConcurrent(n)` | Limit concurrent inbound handler calls |

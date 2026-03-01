╔══════════════════════════════════════════════════════════════════════════════╗
║  channels — Bidirectional multi-platform messaging with SQLite hot-reload  ║
╠══════════════════════════════════════════════════════════════════════════════╣
║                                                                            ║
║  FILE MAP                                                                  ║
║  ────────                                                                  ║
║  channel.go    Channel interface, Message, Attachment, ChannelFactory,     ║
║                InboundHandler, Direction, ChannelStatus                    ║
║  dispatcher.go Dispatcher (lifecycle, routing, dispatch loop)              ║
║  watcher.go    Watch loop (PRAGMA data_version polling)                    ║
║  admin.go      Admin CRUD on channels table                                ║
║  inspect.go    ListChannels iterator, Inspect single channel               ║
║  schema.go     DDL for channels table, OpenDB, Init                        ║
║  errors.go     ErrChannelNotFound, ErrSendFailed, etc.                     ║
║  discord.go    Discord platform (ChannelFactory + discordChannel)          ║
║  telegram.go   Telegram platform (ChannelFactory + telegramChannel)        ║
║  whatsapp.go   WhatsApp platform (ChannelFactory + whatsAppChannel)        ║
║  webhook.go    Generic HTTP webhook (ChannelFactory + webhookChannel)       ║
║                                                                            ║
║  ARCHITECTURE                                                              ║
║  ────────────                                                              ║
║                                                                            ║
║  ┌───────────────────── SQLite DB ──────────────────────┐                  ║
║  │  channels table                                      │                  ║
║  │  ┌──────────┬──────────┬───────┬────────┬──────────┐ │                  ║
║  │  │ name     │ platform │enabled│ config │auth_state│ │                  ║
║  │  ├──────────┼──────────┼───────┼────────┼──────────┤ │                  ║
║  │  │ wa_main  │ whatsapp │   1   │ {...}  │ {...}    │ │                  ║
║  │  │ tg_ops   │ telegram │   1   │ {...}  │ {...}    │ │                  ║
║  │  │ dc_dev   │ discord  │   0   │ {...}  │ {...}    │ │                  ║
║  │  │ hook_ext │ webhook  │   1   │ {...}  │ {}       │ │                  ║
║  │  └──────────┴──────────┴───────┴────────┴──────────┘ │                  ║
║  └───────────────────────┬──────────────────────────────┘                  ║
║         PRAGMA            │                                                ║
║         data_version      │ poll every N ms                                ║
║         changed?          │                                                ║
║              ┌────────────┘                                                ║
║              ▼                                                             ║
║  ┌─────────────────────────────────────────────────────────────────┐       ║
║  │                        Dispatcher                               │       ║
║  │                                                                 │       ║
║  │  Watch(ctx, db, interval)  ─→  Reload(ctx, db)                  │       ║
║  │       │                              │                          │       ║
║  │       │ detects data_version         │ reconcile:               │       ║
║  │       │ change → triggers            │  - start new/changed     │       ║
║  │       │ Reload()                     │  - close removed/disabled│       ║
║  │       │                              │  - fingerprint = platform│       ║
║  │       │                              │    + config (not auth)   │       ║
║  │       │                              ▼                          │       ║
║  │  ┌─────────────────────────────────────────────────┐            │       ║
║  │  │ Active Channels (map[name]*channelEntry)        │            │       ║
║  │  │                                                 │            │       ║
║  │  │  wa_main ──→ whatsAppChannel.Listen(ctx)        │            │       ║
║  │  │  tg_ops  ──→ telegramChannel.Listen(ctx)        │            │       ║
║  │  │  hook_ext──→ webhookChannel.Listen(ctx)         │            │       ║
║  │  └────────────────────┬────────────────────────────┘            │       ║
║  │                       │ <-chan Message (inbound)                 │       ║
║  │                       ▼                                         │       ║
║  │            ┌──────────────────────┐                             │       ║
║  │            │  dispatch() loop     │                             │       ║
║  │            │  per channel         │                             │       ║
║  │            │                      │                             │       ║
║  │  inbound   │  1. receive Message  │                             │       ║
║  │  Message ──│→ 2. sem acquire?     │                             │       ║
║  │            │  3. handler(ctx,msg) ──→ InboundHandler            │       ║
║  │            │  4. sem release?     │   (LLM pipeline, etc.)      │       ║
║  │            │  5. for each resp:   │                             │       ║
║  │            │     ch.Send(resp)    │   returns []Message          │       ║
║  │            └──────────────────────┘                             │       ║
║  │                                                                 │       ║
║  │  Outbound (direct):   d.Send(ctx, msg)                          │       ║
║  │     lookup channel by msg.ChannelName → ch.Send(ctx, msg)       │       ║
║  └─────────────────────────────────────────────────────────────────┘       ║
║                                                                            ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  DATABASE TABLE: channels                                                  ║
║  ────────────────────────                                                  ║
║  name       TEXT PK                                                        ║
║  platform   TEXT NOT NULL  CHECK(IN whatsapp,telegram,discord,             ║
║                                    signal,webhook,matrix)                  ║
║  enabled    INTEGER (0|1) DEFAULT 1                                        ║
║  config     TEXT DEFAULT '{}'    -- per-platform JSON credentials/settings  ║
║  auth_state TEXT DEFAULT '{}'    -- runtime auth (session, pairing state)   ║
║  updated_at INTEGER DEFAULT now  -- auto-updated by trigger                ║
║                                                                            ║
║  Index: platform                                                           ║
║  Trigger: trg_channels_updated_at (auto-update updated_at on UPDATE)       ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  PLATFORM CONFIGS                                                          ║
║  ────────────────                                                          ║
║  WhatsApp:  {"device_name":"...", "store_path":"..."}                       ║
║  Telegram:  {"bot_token":"...", "use_mtproto":false, "webhook_url":""}      ║
║  Discord:   {"bot_token":"...", "guild_id":"", "channel_ids":["..."]}       ║
║  Webhook:   {"listen_addr":":8080", "path":"/webhook/inbound",             ║
║              "secret":"hmac_key", "max_body_bytes":1048576}                ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  CHANNEL INTERFACE                                                         ║
║  ─────────────────                                                         ║
║  Channel {                                                                 ║
║      Listen(ctx) <-chan Message    -- inbound messages until ctx/Close      ║
║      Send(ctx, msg Message) error -- push outbound to platform             ║
║      Status() ChannelStatus       -- connected, auth_state, last_msg       ║
║      Close() error                -- shutdown, releases resources           ║
║  }                                                                         ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED TYPES                                                            ║
║  ──────────────                                                            ║
║  Message      { ID, ChannelName, Platform, SenderID, RecipientID,          ║
║                 Text, ReplyTo string; Direction; Attachments;               ║
║                 Metadata map[string]string; Timestamp time.Time }          ║
║  Attachment   { Type, URL, MimeType, Caption, Filename string }            ║
║  Direction    int (Inbound=0 | Outbound=1)                                 ║
║  ChannelStatus{ Connected bool; Platform, AuthState, Error string;         ║
║                 LastMessage time.Time }                                     ║
║  ChannelInfo  { Name, Platform string; Status ChannelStatus;               ║
║                 Connected bool }                                           ║
║  ChannelRow   { Name, Platform string; Enabled bool;                       ║
║                 Config, AuthState json.RawMessage; UpdatedAt int64 }       ║
║  ChannelFactory  func(name string, config json.RawMessage) (Channel, error)║
║  InboundHandler  func(ctx, Message) ([]Message, error)                     ║
║  Dispatcher       -- manages channel lifecycle, dispatch loop              ║
║  DispatcherOption func(*Dispatcher)                                        ║
║  Admin            -- CRUD on channels table                                ║
║                                                                            ║
║  Error types:                                                              ║
║  ErrChannelNotFound  { Channel string }                                    ║
║  ErrNoPlatformFactory{ Channel, Platform string }                          ║
║  ErrChannelDisabled  { Channel string }                                    ║
║  ErrSendFailed       { Channel, Platform string; Cause error }             ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED FUNCTIONS                                                        ║
║  ──────────────────                                                        ║
║  -- Dispatcher --                                                          ║
║  NewDispatcher(handler InboundHandler, opts ...DispatcherOption) *Disp.     ║
║  (d) RegisterPlatform(platform string, f ChannelFactory)                   ║
║  (d) Reload(ctx, db *sql.DB) error    -- reconcile active channels         ║
║  (d) Watch(ctx, db, interval)         -- poll data_version + auto-reload   ║
║  (d) Send(ctx, msg Message) error     -- direct outbound send              ║
║  (d) Status(name) (ChannelStatus, bool)                                    ║
║  (d) ListChannels() iter.Seq[ChannelInfo]                                  ║
║  (d) Inspect(name) (ChannelInfo, bool)                                     ║
║  (d) Close() error                    -- shutdown all channels             ║
║  WithLogger(l *slog.Logger) DispatcherOption                               ║
║  WithMaxConcurrent(n int) DispatcherOption                                 ║
║                                                                            ║
║  -- Admin --                                                               ║
║  NewAdmin(db *sql.DB) *Admin                                               ║
║  (a) ListChannels(ctx) ([]ChannelRow, error)                               ║
║  (a) GetChannel(ctx, name) (*ChannelRow, error)                            ║
║  (a) UpsertChannel(ctx, name, platform, enabled, config) error             ║
║  (a) DeleteChannel(ctx, name) error                                        ║
║  (a) SetEnabled(ctx, name, enabled) error                                  ║
║  (a) UpdateAuthState(ctx, name, authState) error                           ║
║                                                                            ║
║  -- Schema --                                                              ║
║  OpenDB(path string) (*sql.DB, error)    -- via dbopen.Open                ║
║  Init(db *sql.DB) error                  -- CREATE TABLE channels          ║
║                                                                            ║
║  -- Factories --                                                           ║
║  WhatsAppFactory() ChannelFactory                                          ║
║  TelegramFactory() ChannelFactory                                          ║
║  DiscordFactory() ChannelFactory                                           ║
║  WebhookFactory() ChannelFactory                                           ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  WEBHOOK SPECIFICS                                                         ║
║  ─────────────────                                                         ║
║  Inbound:  HTTP POST → JSON Message body → buffer (256)                    ║
║            HMAC-SHA256 verification via X-Signature-256 header             ║
║            (supports "sha256=" prefix, GitHub-style)                       ║
║  Outbound: POST JSON to msg.Metadata["callback_url"]                       ║
║            SSRF guard: horosafe.ValidateURL blocks private/loopback        ║
║            Signs outbound payload with X-Signature-256 if secret set       ║
║            No callback_url = silently dropped                              ║
║  Server:   ReadHeaderTimeout=10s, Read/Write=30s, Idle=60s                 ║
║            MaxHeaderBytes=64KiB, MaxBody=config or 1MB                     ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES (internal)                                                   ║
║  ────────────────────────                                                  ║
║  github.com/hazyhaar/pkg/dbopen    -- OpenDB with pragmas                  ║
║  github.com/hazyhaar/pkg/horosafe  -- ValidateURL (SSRF guard in webhook)  ║
╠══════════════════════════════════════════════════════════════════════════════╣
║  INVARIANTS                                                                ║
║  ──────────                                                                ║
║  - Channels are driven by SQLite table — modify table, Watch detects       ║
║  - Watch uses PRAGMA data_version (poll interval) — no FS watcher          ║
║  - Fingerprint = platform|config (excludes auth_state) — auth change       ║
║    does NOT restart channel (runtime SDK manages its own session)           ║
║  - closeEntry cancels context AND waits WaitGroup (no goroutine leaks)     ║
║  - Lifecycle context is independent of request context — channels          ║
║    survive beyond short-lived Reload calls                                 ║
║  - WithMaxConcurrent limits concurrent InboundHandler via semaphore        ║
║  - UpsertChannel preserves auth_state on conflict (sessions not lost)      ║
║  - Discord/Telegram/WhatsApp are stubs (TODO markers for SDK wiring)       ║
║  - Webhook is fully implemented (HTTP server + HMAC + SSRF guard)          ║
╚══════════════════════════════════════════════════════════════════════════════╝

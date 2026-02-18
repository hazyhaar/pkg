package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
)

// channelEntry holds a running channel and its config fingerprint.
type channelEntry struct {
	channel     Channel
	cancel      context.CancelFunc
	wg          sync.WaitGroup // tracks the dispatch goroutine
	platform    string
	fingerprint string
}

// Dispatcher manages active channels and routes inbound messages through a
// processing pipeline. It watches the SQLite channels table for changes and
// creates/closes channels accordingly.
//
// The Dispatcher is the integration point between the channels package
// (bidirectional messaging) and the connectivity package (request-response
// routing). The InboundHandler typically calls connectivity.Router.Call to
// process messages through an LLM pipeline.
type Dispatcher struct {
	mu        sync.RWMutex
	channels  map[string]*channelEntry
	factories map[string]ChannelFactory
	handler   InboundHandler
	logger    *slog.Logger

	// lifecycleCtx is a long-lived context that parents all channel listen
	// contexts. It is independent of any request context passed to Reload,
	// so that channels survive beyond a single Reload call.
	lifecycleCtx    context.Context
	lifecycleCancel context.CancelFunc

	// sem is a semaphore channel used when maxConcurrent > 0 to limit
	// concurrent InboundHandler calls.
	sem chan struct{}
}

// DispatcherOption configures a Dispatcher.
type DispatcherOption func(*Dispatcher)

// WithLogger sets a custom logger for the dispatcher.
func WithLogger(l *slog.Logger) DispatcherOption {
	return func(d *Dispatcher) { d.logger = l }
}

// WithMaxConcurrent sets the maximum number of concurrent InboundHandler
// calls across all channels. Use this to prevent unbounded goroutine growth
// when a high-throughput channel (e.g. WhatsApp group) produces messages
// faster than the handler (typically an LLM call) can process them.
// Zero or negative means unlimited (default).
func WithMaxConcurrent(n int) DispatcherOption {
	return func(d *Dispatcher) {
		if n > 0 {
			d.sem = make(chan struct{}, n)
		}
	}
}

// NewDispatcher creates a Dispatcher with the given inbound handler.
// Register platform factories before calling Watch.
func NewDispatcher(handler InboundHandler, opts ...DispatcherOption) *Dispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Dispatcher{
		channels:        make(map[string]*channelEntry),
		factories:       make(map[string]ChannelFactory),
		handler:         handler,
		logger:          slog.Default(),
		lifecycleCtx:    ctx,
		lifecycleCancel: cancel,
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// RegisterPlatform registers a ChannelFactory for a platform name.
// Must be called before Watch. Example: d.RegisterPlatform("whatsapp", WhatsAppFactory())
func (d *Dispatcher) RegisterPlatform(platform string, f ChannelFactory) {
	d.mu.Lock()
	d.factories[platform] = f
	d.mu.Unlock()
}

// Send sends an outbound message through the named channel.
// Returns ErrChannelNotFound if the channel doesn't exist or is not active.
func (d *Dispatcher) Send(ctx context.Context, msg Message) error {
	d.mu.RLock()
	entry, ok := d.channels[msg.ChannelName]
	d.mu.RUnlock()

	if !ok {
		return &ErrChannelNotFound{Channel: msg.ChannelName}
	}
	return entry.channel.Send(ctx, msg)
}

// Status returns the ChannelStatus for a named channel.
// Returns ok=false if the channel is not active.
func (d *Dispatcher) Status(name string) (ChannelStatus, bool) {
	d.mu.RLock()
	entry, ok := d.channels[name]
	d.mu.RUnlock()

	if !ok {
		return ChannelStatus{}, false
	}
	return entry.channel.Status(), true
}

// channelRow is an internal representation of a row in the channels table.
type channelRow struct {
	Name      string
	Platform  string
	Enabled   bool
	Config    json.RawMessage
	AuthState json.RawMessage
}

// fingerprint returns a string that changes when the channel config changes.
//
// Intentionally excludes auth_state: authentication state is managed by
// platform SDKs at runtime (e.g. whatsmeow manages its own session SQLite).
// The auth_state column in the channels table is a backup/export, not a
// reload trigger. Changing auth_state via Admin.UpdateAuthState does not
// restart the channel — which is correct because the running SDK already
// holds the live session.
func (cr channelRow) fingerprint() string {
	return cr.Platform + "|" + string(cr.Config)
}

// Reload reads the channels table and reconciles the active channel set.
// New enabled channels are started, removed or disabled channels are closed,
// and channels with changed config are restarted.
//
// Channel listen contexts are parented to the Dispatcher's lifecycle context,
// not the ctx passed here. This ensures that channels survive beyond a
// short-lived request context (e.g. an admin HTTP handler with a timeout).
func (d *Dispatcher) Reload(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx,
		`SELECT name, platform, enabled, COALESCE(config, '{}'), COALESCE(auth_state, '{}') FROM channels`)
	if err != nil {
		return fmt.Errorf("channels: query channels: %w", err)
	}
	defer rows.Close()

	desired := make(map[string]channelRow)
	for rows.Next() {
		var cr channelRow
		var cfgStr, authStr string
		var enabled int
		if err := rows.Scan(&cr.Name, &cr.Platform, &enabled, &cfgStr, &authStr); err != nil {
			return fmt.Errorf("channels: scan channel: %w", err)
		}
		cr.Enabled = enabled == 1
		cr.Config = json.RawMessage(cfgStr)
		cr.AuthState = json.RawMessage(authStr)
		desired[cr.Name] = cr
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("channels: rows: %w", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// Close channels that were removed or disabled.
	for name, entry := range d.channels {
		cr, exists := desired[name]
		if !exists || !cr.Enabled {
			d.closeEntry(name, entry)
			delete(d.channels, name)
			continue
		}
		// Close and recreate if fingerprint changed.
		if cr.fingerprint() != entry.fingerprint {
			d.closeEntry(name, entry)
			delete(d.channels, name)
		}
	}

	// Start new or restarted channels.
	for name, cr := range desired {
		if !cr.Enabled {
			continue
		}
		if _, active := d.channels[name]; active {
			// Already running with same fingerprint.
			continue
		}

		factory, ok := d.factories[cr.Platform]
		if !ok {
			d.logger.Warn("no factory for platform",
				"channel", name, "platform", cr.Platform)
			continue
		}

		ch, err := factory(name, cr.Config)
		if err != nil {
			d.logger.Error("channel factory failed",
				"channel", name, "platform", cr.Platform, "error", err)
			continue
		}

		// Use the dispatcher's lifecycle context, not the request ctx.
		listenCtx, cancel := context.WithCancel(d.lifecycleCtx)
		entry := &channelEntry{
			channel:     ch,
			cancel:      cancel,
			platform:    cr.Platform,
			fingerprint: cr.fingerprint(),
		}
		d.channels[name] = entry

		// Start listening for inbound messages, tracked by WaitGroup.
		entry.wg.Add(1)
		go d.dispatch(listenCtx, name, ch, &entry.wg)

		d.logger.Info("channel started",
			"channel", name, "platform", cr.Platform)
	}

	d.logger.Info("channels reloaded",
		"active", len(d.channels),
		"configured", len(desired))

	return nil
}

// dispatch reads inbound messages from a channel and processes them through
// the InboundHandler. Outbound responses are sent back through the same channel.
func (d *Dispatcher) dispatch(ctx context.Context, name string, ch Channel, wg *sync.WaitGroup) {
	defer wg.Done()
	msgs := ch.Listen(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-msgs:
			if !ok {
				d.logger.Info("channel listen closed", "channel", name)
				return
			}

			// Acquire semaphore slot if concurrency is limited.
			if d.sem != nil {
				select {
				case d.sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
			}

			responses, err := d.handler(ctx, msg)

			if d.sem != nil {
				<-d.sem
			}

			if err != nil {
				d.logger.Error("inbound handler failed",
					"channel", name, "sender", msg.SenderID, "error", err)
				continue
			}

			for _, resp := range responses {
				resp.ChannelName = name
				resp.Direction = Outbound
				if err := ch.Send(ctx, resp); err != nil {
					d.logger.Error("send response failed",
						"channel", name, "recipient", resp.RecipientID, "error", err)
				}
			}
		}
	}
}

// closeEntry shuts down a channel entry and waits for its dispatch goroutine
// to exit before returning, preventing goroutine leaks on rapid reconnect.
func (d *Dispatcher) closeEntry(name string, entry *channelEntry) {
	entry.cancel()
	if err := entry.channel.Close(); err != nil {
		d.logger.Error("channel close failed",
			"channel", name, "platform", entry.platform, "error", err)
	} else {
		d.logger.Info("channel stopped",
			"channel", name, "platform", entry.platform)
	}
	entry.wg.Wait()
}

// Close shuts down all active channels and cancels the lifecycle context.
func (d *Dispatcher) Close() error {
	d.lifecycleCancel()
	d.mu.Lock()
	defer d.mu.Unlock()
	for name, entry := range d.channels {
		d.closeEntry(name, entry)
	}
	d.channels = make(map[string]*channelEntry)
	return nil
}

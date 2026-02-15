// Package connectivity provides a smart service router that dispatches calls
// either locally (in-memory function call, ~0.01ms) or remotely (QUIC/HTTP,
// ~50ms) based on a SQLite routes table reloaded at runtime.
//
// This implements the "Job as Library" pattern: you code as a monolith,
// deploy as microservices, and switch between the two by updating one SQL row.
//
//	router := connectivity.New()
//	router.RegisterTransport("quic", myQuicFactory)
//	router.RegisterLocal("billing", billingService.Process)
//	go router.Watch(ctx, db, 200*time.Millisecond)
//
//	// Caller doesn't know or care whether this is local or remote:
//	resp, err := router.Call(ctx, "billing", payload)
//
// The routes table in SQLite decides the strategy. Change it at runtime
// and the next Call picks up the new route — zero downtime, zero restart.
package connectivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Handler is a transport-agnostic service function: bytes in, bytes out.
// Both local Go functions and remote RPC clients implement this signature.
type Handler func(ctx context.Context, payload []byte) ([]byte, error)

// TransportFactory creates a Handler for a given remote endpoint.
// It receives the endpoint URL (e.g. "quic://10.0.0.5:443") and any
// per-route config JSON. The returned close function is called when the
// route is removed or replaced during hot-reload; it may be nil if no
// cleanup is needed.
type TransportFactory func(endpoint string, config json.RawMessage) (handler Handler, close func(), err error)

// route is an internal representation of a row in the routes table.
type route struct {
	ServiceName string
	Strategy    string
	Endpoint    string
	Config      json.RawMessage
}

// fingerprint returns a string that changes when the route config changes.
func (rt route) fingerprint() string {
	return rt.Strategy + "|" + rt.Endpoint + "|" + string(rt.Config)
}

// remoteEntry holds a handler and its optional cleanup function.
type remoteEntry struct {
	handler Handler
	close   func()
}

// Router dispatches service calls based on SQLite configuration.
// Thread-safe: reads use RLock, reloads use full Lock.
type Router struct {
	mu            sync.RWMutex
	localHandlers map[string]Handler
	remoteEntries map[string]remoteEntry
	routeSnap     map[string]route // last loaded snapshot for diffing
	factories     map[string]TransportFactory
	logger        *slog.Logger
}

// Option configures a Router.
type Option func(*Router)

// WithLogger sets a custom logger for the router.
func WithLogger(l *slog.Logger) Option {
	return func(r *Router) { r.logger = l }
}

// New creates a Router with no routes. Register transports and local handlers,
// then call Watch to start hot-reloading from SQLite.
func New(opts ...Option) *Router {
	r := &Router{
		localHandlers: make(map[string]Handler),
		remoteEntries: make(map[string]remoteEntry),
		routeSnap:     make(map[string]route),
		factories:     make(map[string]TransportFactory),
		logger:        slog.Default(),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// RegisterLocal registers an in-memory handler for a service.
// This is the "Job as Library" side: the function lives in the same binary.
// If the routes table says strategy="local" for this service, Call dispatches
// here with zero network overhead.
func (r *Router) RegisterLocal(service string, h Handler) {
	r.mu.Lock()
	r.localHandlers[service] = h
	r.mu.Unlock()
}

// RegisterTransport registers a factory for a transport protocol.
// Example protocols: "quic", "http", "grpc", "mcp".
// The factory is called during Reload when a route uses this protocol.
func (r *Router) RegisterTransport(protocol string, f TransportFactory) {
	r.mu.Lock()
	r.factories[protocol] = f
	r.mu.Unlock()
}

// Call dispatches a service call. The resolution order is:
//  1. Noop route — silently succeeds (feature flag / service disabled).
//  2. Explicit remote route (from SQLite) — if strategy is "quic", "http", etc.
//  3. Local handler — if strategy is "local" or no remote route exists.
//  4. Error — service not routable.
//
// Callers never need to know whether the call is local or remote.
func (r *Router) Call(ctx context.Context, service string, payload []byte) ([]byte, error) {
	r.mu.RLock()
	entry, hasRemote := r.remoteEntries[service]
	localH := r.localHandlers[service]
	snap, hasRoute := r.routeSnap[service]
	r.mu.RUnlock()

	// Noop: silently succeed without doing anything.
	if hasRoute && snap.Strategy == "noop" {
		r.logger.DebugContext(ctx, "routing noop", "service", service)
		return nil, nil
	}

	// Remote route takes priority (SQLite says so).
	if hasRemote {
		r.logger.DebugContext(ctx, "routing remote",
			"service", service, "strategy", snap.Strategy, "endpoint", snap.Endpoint)
		return entry.handler(ctx, payload)
	}

	// Fallback to local handler.
	if localH != nil {
		r.logger.DebugContext(ctx, "routing local", "service", service)
		return localH(ctx, payload)
	}

	return nil, &ErrServiceNotFound{Service: service}
}

// Reload reads the routes table and rebuilds the remote handler map.
// Routes with strategy "local" or "noop" do not create remote handlers.
// Only routes whose (strategy, endpoint, config) changed are rebuilt,
// preserving existing connections for unchanged routes.
func (r *Router) Reload(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx,
		`SELECT service_name, strategy, COALESCE(endpoint, ''), COALESCE(config, '{}') FROM routes`)
	if err != nil {
		return fmt.Errorf("connectivity: query routes: %w", err)
	}
	defer rows.Close()

	newRoutes := make(map[string]route)
	for rows.Next() {
		var rt route
		var cfgStr string
		if err := rows.Scan(&rt.ServiceName, &rt.Strategy, &rt.Endpoint, &cfgStr); err != nil {
			return fmt.Errorf("connectivity: scan route: %w", err)
		}
		rt.Config = json.RawMessage(cfgStr)
		newRoutes[rt.ServiceName] = rt
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("connectivity: rows: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	newEntries := make(map[string]remoteEntry, len(newRoutes))

	for name, rt := range newRoutes {
		switch rt.Strategy {
		case "local", "noop":
			// No remote handler needed.
			continue
		default:
			// Check if the route is unchanged — reuse existing entry.
			if old, ok := r.routeSnap[name]; ok && old.fingerprint() == rt.fingerprint() {
				if existing, exists := r.remoteEntries[name]; exists {
					newEntries[name] = existing
					continue
				}
			}

			// Build new handler via factory.
			factory, ok := r.factories[rt.Strategy]
			if !ok {
				r.logger.Warn("no transport factory for strategy",
					"service", name, "strategy", rt.Strategy)
				continue
			}

			h, closeFn, err := factory(rt.Endpoint, rt.Config)
			if err != nil {
				r.logger.Error("factory failed",
					"service", name, "strategy", rt.Strategy,
					"endpoint", rt.Endpoint, "error", err)
				continue
			}
			newEntries[name] = remoteEntry{handler: h, close: closeFn}
			r.logger.Info("route built",
				"service", name, "strategy", rt.Strategy, "endpoint", rt.Endpoint)
		}
	}

	// Close old entries that were removed or whose config changed.
	for name, old := range r.remoteEntries {
		if old.close == nil {
			continue
		}
		if _, stillExists := newEntries[name]; !stillExists {
			// Route was removed entirely.
			old.close()
			continue
		}
		// Route still exists — close old handler if fingerprint changed
		// (a new handler was already built above).
		oldSnap := r.routeSnap[name]
		newRt := newRoutes[name]
		if oldSnap.fingerprint() != newRt.fingerprint() {
			old.close()
		}
	}

	r.remoteEntries = newEntries
	r.routeSnap = newRoutes

	r.logger.Info("routes reloaded",
		"total", len(newRoutes),
		"remote", len(newEntries),
		"local", countLocal(newRoutes))

	return nil
}

// Close shuts down all remote handlers.
func (r *Router) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range r.remoteEntries {
		if entry.close != nil {
			entry.close()
		}
	}
	r.remoteEntries = make(map[string]remoteEntry)
	r.routeSnap = make(map[string]route)
	return nil
}

func countLocal(routes map[string]route) int {
	n := 0
	for _, rt := range routes {
		if rt.Strategy == "local" {
			n++
		}
	}
	return n
}

// callTimeout extracts timeout from route config, with a default.
func callTimeout(cfg json.RawMessage, defaultTimeout time.Duration) time.Duration {
	var parsed struct {
		TimeoutMs int64 `json:"timeout_ms"`
	}
	if json.Unmarshal(cfg, &parsed) == nil && parsed.TimeoutMs > 0 {
		return time.Duration(parsed.TimeoutMs) * time.Millisecond
	}
	return defaultTimeout
}

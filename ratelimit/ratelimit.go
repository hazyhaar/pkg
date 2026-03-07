// CLAUDE:SUMMARY Generic transport-agnostic rate limiter with SQLite-backed rules and in-memory sliding window buckets.
// CLAUDE:DEPENDS
// CLAUDE:EXPORTS Limiter, New, RuleConfig, RuleEntry, Option, WithClock, ErrRateLimited, MCPMiddlewareFunc

// Package ratelimit provides a generic, transport-agnostic rate limiter
// backed by SQLite. It can rate-limit by any key: tool name, user ID, IP
// address, or any composite key.
//
// The limiter uses a sliding window counter stored in SQLite for persistence
// and an in-memory fast path for hot keys.
package ratelimit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrRateLimited is returned when a rate limit is exceeded.
var ErrRateLimited = errors.New("ratelimit: rate limit exceeded")

// Schema creates the rate limiter configuration table.
const Schema = `
CREATE TABLE IF NOT EXISTS rate_limiter_rules (
	rule_key TEXT PRIMARY KEY,
	max_requests INTEGER NOT NULL DEFAULT 60,
	window_seconds INTEGER NOT NULL DEFAULT 60,
	is_active INTEGER NOT NULL DEFAULT 1 CHECK(is_active IN (0, 1)),
	created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
	updated_at INTEGER
);
`

// bucket tracks request counts for a key within a time window.
type bucket struct {
	count   int
	resetAt time.Time
}

// Limiter is a generic rate limiter with SQLite-backed configuration
// and in-memory token buckets.
type Limiter struct {
	db      *sql.DB
	rules   map[string]RuleConfig
	buckets sync.Map
	mu      sync.RWMutex
	now     func() time.Time // injectable clock
}

// RuleConfig defines the rate limit for a single key pattern.
type RuleConfig struct {
	MaxRequests   int
	WindowSeconds int
	IsActive      bool
}

// Option configures a Limiter.
type Option func(*Limiter)

// WithClock sets a custom clock function (for testing).
func WithClock(fn func() time.Time) Option {
	return func(l *Limiter) { l.now = fn }
}

// New creates a Limiter backed by the given database.
// Call Init() to create tables, then Reload() to load rules.
func New(db *sql.DB, opts ...Option) *Limiter {
	l := &Limiter{
		db:    db,
		rules: make(map[string]RuleConfig),
		now:   time.Now,
	}
	for _, o := range opts {
		o(l)
	}
	return l
}

// Init creates the rate_limiter_rules table.
func (l *Limiter) Init() error {
	_, err := l.db.Exec(Schema)
	return err
}

// Reload loads all active rules from the database.
func (l *Limiter) Reload() error {
	rows, err := l.db.Query(`SELECT rule_key, max_requests, window_seconds, is_active FROM rate_limiter_rules WHERE is_active = 1`)
	if err != nil {
		return fmt.Errorf("ratelimit: reload: %w", err)
	}
	defer rows.Close()

	rules := make(map[string]RuleConfig)
	for rows.Next() {
		var key string
		var cfg RuleConfig
		var active int
		if err := rows.Scan(&key, &cfg.MaxRequests, &cfg.WindowSeconds, &active); err != nil {
			continue
		}
		cfg.IsActive = active == 1
		rules[key] = cfg
	}

	l.mu.Lock()
	l.rules = rules
	l.mu.Unlock()

	slog.Debug("ratelimit: rules reloaded", "count", len(rules))
	return rows.Err()
}

// StartReloader starts background goroutines for rule reloading (every 60s)
// and bucket GC (every 5min). Stops when ctx is cancelled.
func (l *Limiter) StartReloader(ctx context.Context) {
	reloadTick := time.NewTicker(60 * time.Second)
	gcTick := time.NewTicker(5 * time.Minute)
	go func() {
		defer reloadTick.Stop()
		defer gcTick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-reloadTick.C:
				if err := l.Reload(); err != nil {
					slog.Warn("ratelimit: reload failed", "error", err)
				}
			case <-gcTick.C:
				l.gc()
			}
		}
	}()
}

func (l *Limiter) gc() {
	now := l.now()
	l.buckets.Range(func(key, value any) bool {
		b := value.(*bucket)
		if now.After(b.resetAt) {
			l.buckets.Delete(key)
		}
		return true
	})
}

// Allow checks and consumes a token for the given key.
// Uses the provided limit and window if no rule is configured in the database
// for this key.
// Returns nil if allowed, ErrRateLimited if exhausted.
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) error {
	// Check DB-configured rules first (override programmatic defaults).
	l.mu.RLock()
	if cfg, ok := l.rules[key]; ok && cfg.IsActive {
		limit = cfg.MaxRequests
		window = time.Duration(cfg.WindowSeconds) * time.Second
	}
	l.mu.RUnlock()

	now := l.now()

	val, loaded := l.buckets.LoadOrStore(key, &bucket{
		count:   1,
		resetAt: now.Add(window),
	})
	if !loaded {
		return nil
	}

	b := val.(*bucket)
	if now.After(b.resetAt) {
		b.count = 1
		b.resetAt = now.Add(window)
		return nil
	}

	b.count++
	if b.count > limit {
		return ErrRateLimited
	}
	return nil
}

// AllowN checks and consumes n tokens for the given key.
func (l *Limiter) AllowN(ctx context.Context, key string, n int, limit int, window time.Duration) error {
	for i := 0; i < n; i++ {
		if err := l.Allow(ctx, key, limit, window); err != nil {
			return err
		}
	}
	return nil
}

// AddRule inserts or updates a rule in the database.
// Call Reload() to pick up changes.
func (l *Limiter) AddRule(key string, maxRequests, windowSeconds int) error {
	_, err := l.db.Exec(`INSERT INTO rate_limiter_rules (rule_key, max_requests, window_seconds)
		VALUES (?, ?, ?)
		ON CONFLICT(rule_key) DO UPDATE SET max_requests=excluded.max_requests,
		window_seconds=excluded.window_seconds, updated_at=strftime('%s','now'), is_active=1`,
		key, maxRequests, windowSeconds)
	return err
}

// RemoveRule deactivates a rule in the database.
func (l *Limiter) RemoveRule(key string) error {
	_, err := l.db.Exec(`UPDATE rate_limiter_rules SET is_active = 0, updated_at = strftime('%s','now') WHERE rule_key = ?`, key)
	return err
}

// --- HTTP Middleware ---

// HTTPMiddleware returns an HTTP middleware that rate-limits by client IP.
// The key format is "ip:{client_ip}".
func (l *Limiter) HTTPMiddleware(limit int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := extractIP(r)
			key := "ip:" + ip

			if err := l.Allow(r.Context(), key, limit, window); err != nil {
				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(window.Seconds())))
				if strings.HasPrefix(r.URL.Path, "/api/") {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusTooManyRequests)
					fmt.Fprintf(w, `{"error":"rate limit exceeded"}`)
				} else {
					http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// extractIP returns the client IP from X-Forwarded-For or RemoteAddr.
func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return strings.TrimSpace(xff[:i])
			}
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// --- MCP Middleware ---

// MCPMiddlewareFunc is a function that wraps tool execution with rate limiting.
// It takes the tool name as the rate limit key.
type MCPMiddlewareFunc func(ctx context.Context, toolName string) error

// MCPMiddleware returns a function that can be used as a PolicyFunc in mcprt.
// The key format is "tool:{tool_name}".
func (l *Limiter) MCPMiddleware(limit int, window time.Duration) MCPMiddlewareFunc {
	return func(ctx context.Context, toolName string) error {
		key := "tool:" + toolName
		return l.Allow(ctx, key, limit, window)
	}
}

// ListRules returns all rules (active and inactive).
func (l *Limiter) ListRules() ([]RuleEntry, error) {
	rows, err := l.db.Query(`SELECT rule_key, max_requests, window_seconds, is_active FROM rate_limiter_rules ORDER BY rule_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []RuleEntry
	for rows.Next() {
		var e RuleEntry
		var active int
		if err := rows.Scan(&e.Key, &e.MaxRequests, &e.WindowSeconds, &active); err != nil {
			return nil, err
		}
		e.IsActive = active == 1
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// RuleEntry is a row from rate_limiter_rules.
type RuleEntry struct {
	Key           string
	MaxRequests   int
	WindowSeconds int
	IsActive      bool
}

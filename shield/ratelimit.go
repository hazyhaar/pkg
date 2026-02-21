package shield

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimitConfig defines the rate limit for a single endpoint.
type RateLimitConfig struct {
	MaxRequests   int
	WindowSeconds int
	Enabled       bool
}

type bucket struct {
	count   int
	resetAt time.Time
}

// RateLimiter provides per-IP, per-endpoint rate limiting backed by a SQLite
// rate_limits table. Rules are reloaded periodically and expired buckets are
// garbage collected.
//
// Expected schema:
//
//	CREATE TABLE IF NOT EXISTS rate_limits (
//	    endpoint TEXT PRIMARY KEY,
//	    max_requests INTEGER NOT NULL DEFAULT 60,
//	    window_seconds INTEGER NOT NULL DEFAULT 60,
//	    enabled INTEGER NOT NULL DEFAULT 1
//	);
type RateLimiter struct {
	db      *sql.DB
	rules   map[string]RateLimitConfig
	buckets sync.Map
	mu      sync.RWMutex
	exclude []string // path prefixes excluded from rate limiting
}

// NewRateLimiter creates a rate limiter that reads rules from the rate_limits
// table in db. Call StartReloader to enable periodic rule refresh and GC.
func NewRateLimiter(db *sql.DB, excludePrefixes ...string) *RateLimiter {
	rl := &RateLimiter{
		db:      db,
		rules:   make(map[string]RateLimitConfig),
		exclude: excludePrefixes,
	}
	rl.reload()
	return rl
}

// SetDB replaces the database connection and reloads rules.
// Used in FO mode when the dbsync subscriber swaps the database.
func (rl *RateLimiter) SetDB(db *sql.DB) {
	rl.db = db
	rl.reload()
}

// StartReloader starts background goroutines for rule reloading (every 60s)
// and bucket GC (every 5min). Stops when done is closed.
func (rl *RateLimiter) StartReloader(done <-chan struct{}) {
	reloadTick := time.NewTicker(60 * time.Second)
	gcTick := time.NewTicker(5 * time.Minute)
	go func() {
		defer reloadTick.Stop()
		defer gcTick.Stop()
		for {
			select {
			case <-done:
				return
			case <-reloadTick.C:
				rl.reload()
			case <-gcTick.C:
				rl.gc()
			}
		}
	}()
}

func (rl *RateLimiter) reload() {
	rows, err := rl.db.Query(`SELECT endpoint, max_requests, window_seconds, enabled FROM rate_limits`)
	if err != nil {
		slog.Warn("ratelimit: failed to reload rules", "error", err)
		return
	}
	defer rows.Close()

	rules := make(map[string]RateLimitConfig)
	for rows.Next() {
		var endpoint string
		var cfg RateLimitConfig
		var enabled int
		if err := rows.Scan(&endpoint, &cfg.MaxRequests, &cfg.WindowSeconds, &enabled); err != nil {
			continue
		}
		cfg.Enabled = enabled == 1
		rules[endpoint] = cfg
	}

	rl.mu.Lock()
	rl.rules = rules
	rl.mu.Unlock()

	slog.Debug("ratelimit: rules reloaded", "count", len(rules))
}

func (rl *RateLimiter) gc() {
	now := time.Now()
	rl.buckets.Range(func(key, value any) bool {
		b := value.(*bucket)
		if now.After(b.resetAt) {
			rl.buckets.Delete(key)
		}
		return true
	})
}

func (rl *RateLimiter) allow(ip, endpoint string) bool {
	rl.mu.RLock()
	cfg, ok := rl.rules[endpoint]
	rl.mu.RUnlock()

	if !ok || !cfg.Enabled {
		return true
	}

	key := ip + ":" + endpoint
	now := time.Now()

	val, loaded := rl.buckets.LoadOrStore(key, &bucket{
		count:   1,
		resetAt: now.Add(time.Duration(cfg.WindowSeconds) * time.Second),
	})
	if !loaded {
		return true
	}

	b := val.(*bucket)
	if now.After(b.resetAt) {
		b.count = 1
		b.resetAt = now.Add(time.Duration(cfg.WindowSeconds) * time.Second)
		return true
	}

	b.count++
	return b.count <= cfg.MaxRequests
}

// Middleware is the HTTP middleware that enforces rate limits.
// API paths (/api/*) get a 429 JSON response; other paths get a redirect
// with a flash message.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip excluded prefixes.
		for _, prefix := range rl.exclude {
			if strings.HasPrefix(r.URL.Path, prefix) {
				next.ServeHTTP(w, r)
				return
			}
		}

		endpoint := r.Method + " " + r.URL.Path
		ip := ExtractIP(r)

		if rl.allow(ip, endpoint) {
			next.ServeHTTP(w, r)
			return
		}

		slog.Warn("ratelimit: request blocked", "ip", ip, "endpoint", endpoint)

		w.Header().Set("Retry-After", "60")

		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "rate limit exceeded",
			})
			return
		}

		SetFlash(w, "error", "Trop de requêtes, veuillez patienter")
		referer := r.Header.Get("Referer")
		if referer == "" {
			referer = r.URL.Path
		}
		http.Redirect(w, r, referer, http.StatusSeeOther)
	})
}

// ExtractIP returns the client IP from X-Forwarded-For or RemoteAddr.
func ExtractIP(r *http.Request) string {
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

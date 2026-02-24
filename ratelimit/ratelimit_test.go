package ratelimit

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func setupTestLimiter(t *testing.T, opts ...Option) (*sql.DB, *Limiter) {
	t.Helper()
	db := setupTestDB(t)
	l := New(db, opts...)
	if err := l.Init(); err != nil {
		t.Fatal(err)
	}
	return db, l
}

func TestInit_CreatesTable(t *testing.T) {
	db := setupTestDB(t)
	l := New(db)
	if err := l.Init(); err != nil {
		t.Fatal(err)
	}
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='rate_limiter_rules'").Scan(&name)
	if err != nil {
		t.Fatal("rate_limiter_rules table not created")
	}
}

func TestInit_Idempotent(t *testing.T) {
	db := setupTestDB(t)
	l := New(db)
	if err := l.Init(); err != nil {
		t.Fatal(err)
	}
	if err := l.Init(); err != nil {
		t.Fatalf("second Init should be idempotent: %v", err)
	}
}

func TestAllow_UnderLimit(t *testing.T) {
	_, l := setupTestLimiter(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := l.Allow(ctx, "test-key", 5, time.Minute); err != nil {
			t.Fatalf("request %d should be allowed: %v", i+1, err)
		}
	}
}

func TestAllow_ExceedsLimit(t *testing.T) {
	_, l := setupTestLimiter(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		l.Allow(ctx, "test-key", 5, time.Minute)
	}

	err := l.Allow(ctx, "test-key", 5, time.Minute)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("6th request should be rate limited, got: %v", err)
	}
}

func TestAllow_DifferentKeys_Independent(t *testing.T) {
	_, l := setupTestLimiter(t)
	ctx := context.Background()

	// Exhaust key-a.
	for i := 0; i < 3; i++ {
		l.Allow(ctx, "key-a", 3, time.Minute)
	}
	if err := l.Allow(ctx, "key-a", 3, time.Minute); !errors.Is(err, ErrRateLimited) {
		t.Fatal("key-a should be rate limited")
	}

	// key-b should still be available.
	if err := l.Allow(ctx, "key-b", 3, time.Minute); err != nil {
		t.Fatalf("key-b should be allowed: %v", err)
	}
}

func TestAllow_WindowExpiry(t *testing.T) {
	now := time.Now()
	_, l := setupTestLimiter(t, WithClock(func() time.Time { return now }))
	ctx := context.Background()

	// Exhaust the limit.
	for i := 0; i < 3; i++ {
		l.Allow(ctx, "test-key", 3, 10*time.Second)
	}
	if err := l.Allow(ctx, "test-key", 3, 10*time.Second); !errors.Is(err, ErrRateLimited) {
		t.Fatal("should be rate limited")
	}

	// Advance past window.
	now = now.Add(11 * time.Second)
	if err := l.Allow(ctx, "test-key", 3, 10*time.Second); err != nil {
		t.Fatalf("should be allowed after window expiry: %v", err)
	}
}

func TestAllow_DBRuleOverridesDefault(t *testing.T) {
	_, l := setupTestLimiter(t)
	ctx := context.Background()

	// Set a DB rule with limit=2.
	l.AddRule("strict-key", 2, 60)
	l.Reload()

	// programmatic limit is 100, but DB says 2.
	l.Allow(ctx, "strict-key", 100, time.Minute)
	l.Allow(ctx, "strict-key", 100, time.Minute)
	err := l.Allow(ctx, "strict-key", 100, time.Minute)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatal("DB rule should override programmatic limit")
	}
}

func TestAddRule_Upsert(t *testing.T) {
	_, l := setupTestLimiter(t)

	l.AddRule("key", 10, 60)
	l.AddRule("key", 20, 120) // Update.
	l.Reload()

	l.mu.RLock()
	cfg := l.rules["key"]
	l.mu.RUnlock()

	if cfg.MaxRequests != 20 || cfg.WindowSeconds != 120 {
		t.Fatalf("expected 20/120, got %d/%d", cfg.MaxRequests, cfg.WindowSeconds)
	}
}

func TestRemoveRule(t *testing.T) {
	_, l := setupTestLimiter(t)

	l.AddRule("key", 2, 60)
	l.Reload()
	l.RemoveRule("key")
	l.Reload()

	l.mu.RLock()
	_, exists := l.rules["key"]
	l.mu.RUnlock()

	if exists {
		t.Fatal("removed rule should not be loaded")
	}
}

func TestListRules(t *testing.T) {
	_, l := setupTestLimiter(t)

	l.AddRule("a", 10, 60)
	l.AddRule("b", 20, 120)
	l.RemoveRule("b")

	entries, err := l.ListRules()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Key == "a" && !e.IsActive {
			t.Fatal("rule 'a' should be active")
		}
		if e.Key == "b" && e.IsActive {
			t.Fatal("rule 'b' should be inactive")
		}
	}
}

func TestHTTPMiddleware_AllowsUnderLimit(t *testing.T) {
	_, l := setupTestLimiter(t)

	handler := l.HTTPMiddleware(5, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHTTPMiddleware_BlocksOverLimit(t *testing.T) {
	_, l := setupTestLimiter(t)

	handler := l.HTTPMiddleware(2, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/api/test", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	// 3rd request should be blocked.
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header")
	}
}

func TestHTTPMiddleware_DifferentIPs_Independent(t *testing.T) {
	_, l := setupTestLimiter(t)

	handler := l.HTTPMiddleware(1, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First IP uses its one request.
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "1.1.1.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatal("first IP first request should be 200")
	}

	// Second IP should still be allowed.
	req = httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "2.2.2.2:1234"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatal("second IP first request should be 200")
	}
}

func TestMCPMiddleware(t *testing.T) {
	_, l := setupTestLimiter(t)
	ctx := context.Background()

	mw := l.MCPMiddleware(2, time.Minute)

	if err := mw(ctx, "my_tool"); err != nil {
		t.Fatal(err)
	}
	if err := mw(ctx, "my_tool"); err != nil {
		t.Fatal(err)
	}
	if err := mw(ctx, "my_tool"); !errors.Is(err, ErrRateLimited) {
		t.Fatal("3rd call should be rate limited")
	}
}

func TestAllowN(t *testing.T) {
	_, l := setupTestLimiter(t)
	ctx := context.Background()

	// Use 3 tokens at once.
	if err := l.AllowN(ctx, "bulk", 3, 5, time.Minute); err != nil {
		t.Fatal(err)
	}

	// 2 more should work.
	if err := l.AllowN(ctx, "bulk", 2, 5, time.Minute); err != nil {
		t.Fatal(err)
	}

	// 1 more should fail.
	if err := l.AllowN(ctx, "bulk", 1, 5, time.Minute); !errors.Is(err, ErrRateLimited) {
		t.Fatal("should be rate limited after 6 tokens consumed (limit 5)")
	}
}

func TestExtractIP_XFF(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "10.0.0.1, 10.0.0.2")
	ip := extractIP(req)
	if ip != "10.0.0.1" {
		t.Fatalf("extractIP = %q, want %q", ip, "10.0.0.1")
	}
}

func TestExtractIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.1:8080"
	ip := extractIP(req)
	if ip != "192.168.1.1" {
		t.Fatalf("extractIP = %q, want %q", ip, "192.168.1.1")
	}
}

func TestGC_RemovesExpiredBuckets(t *testing.T) {
	now := time.Now()
	_, l := setupTestLimiter(t, WithClock(func() time.Time { return now }))
	ctx := context.Background()

	l.Allow(ctx, "ephemeral", 100, 5*time.Second)

	// Advance past window.
	now = now.Add(10 * time.Second)
	l.gc()

	// Bucket should be gone.
	_, loaded := l.buckets.Load("ephemeral")
	if loaded {
		t.Fatal("expired bucket should be garbage collected")
	}
}

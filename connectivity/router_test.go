package connectivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// setupTestDB creates an in-memory SQLite database with the routes schema.
// MaxOpenConns=1 ensures all operations use the same in-memory database
// (each connection to ":memory:" creates a separate database).
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := Init(db); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestNew(t *testing.T) {
	r := New()
	if r == nil {
		t.Fatal("New returned nil")
	}
	if r.localHandlers == nil || r.remoteEntries == nil || r.factories == nil {
		t.Fatal("maps not initialized")
	}
}

func TestRegisterLocal_and_Call(t *testing.T) {
	r := New()
	called := false
	r.RegisterLocal("echo", func(ctx context.Context, payload []byte) ([]byte, error) {
		called = true
		return payload, nil
	})

	resp, err := r.Call(context.Background(), "echo", []byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("local handler not called")
	}
	if string(resp) != "hello" {
		t.Fatalf("got %q, want %q", resp, "hello")
	}
}

func TestCall_ServiceNotFound(t *testing.T) {
	r := New()
	_, err := r.Call(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var snf *ErrServiceNotFound
	if !errors.As(err, &snf) {
		t.Fatalf("expected ErrServiceNotFound, got %T: %v", err, err)
	}
	if snf.Service != "nonexistent" {
		t.Fatalf("got service %q, want %q", snf.Service, "nonexistent")
	}
}

func TestReload_LocalStrategy(t *testing.T) {
	db := setupTestDB(t)
	r := New()

	localCalled := false
	r.RegisterLocal("billing", func(ctx context.Context, payload []byte) ([]byte, error) {
		localCalled = true
		return []byte("ok"), nil
	})

	// Insert a "local" route.
	_, err := db.Exec(`INSERT INTO routes (service_name, strategy) VALUES ('billing', 'local')`)
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Reload(context.Background(), db); err != nil {
		t.Fatalf("reload: %v", err)
	}

	resp, err := r.Call(context.Background(), "billing", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !localCalled {
		t.Fatal("local handler not called for local strategy")
	}
	if string(resp) != "ok" {
		t.Fatalf("got %q", resp)
	}
}

func TestReload_NoopStrategy(t *testing.T) {
	db := setupTestDB(t)
	r := New()

	// Register a local handler that should NOT be called.
	r.RegisterLocal("disabled", func(ctx context.Context, payload []byte) ([]byte, error) {
		t.Fatal("local handler should not be called for noop")
		return nil, nil
	})

	_, err := db.Exec(`INSERT INTO routes (service_name, strategy) VALUES ('disabled', 'noop')`)
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Reload(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	resp, err := r.Call(context.Background(), "disabled", []byte("data"))
	if err != nil {
		t.Fatalf("noop should succeed, got: %v", err)
	}
	if resp != nil {
		t.Fatalf("noop should return nil, got %q", resp)
	}
}

func TestReload_RemoteStrategy(t *testing.T) {
	db := setupTestDB(t)
	r := New()

	remoteCalled := false
	r.RegisterTransport("http", func(endpoint string, config json.RawMessage) (Handler, func(), error) {
		h := func(ctx context.Context, payload []byte) ([]byte, error) {
			remoteCalled = true
			return []byte("remote:" + endpoint), nil
		}
		return h, nil, nil
	})

	_, err := db.Exec(`INSERT INTO routes (service_name, strategy, endpoint) VALUES ('api', 'http', 'http://10.0.0.1:8080')`)
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Reload(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	resp, err := r.Call(context.Background(), "api", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !remoteCalled {
		t.Fatal("remote handler not called")
	}
	if string(resp) != "remote:http://10.0.0.1:8080" {
		t.Fatalf("got %q", resp)
	}
}

func TestReload_RemoteOverridesLocal(t *testing.T) {
	db := setupTestDB(t)
	r := New()

	r.RegisterLocal("billing", func(ctx context.Context, payload []byte) ([]byte, error) {
		return []byte("local"), nil
	})

	r.RegisterTransport("http", func(endpoint string, config json.RawMessage) (Handler, func(), error) {
		h := func(ctx context.Context, payload []byte) ([]byte, error) {
			return []byte("remote"), nil
		}
		return h, nil, nil
	})

	_, err := db.Exec(`INSERT INTO routes (service_name, strategy, endpoint) VALUES ('billing', 'http', 'http://10.0.0.1:8080')`)
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Reload(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	resp, err := r.Call(context.Background(), "billing", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp) != "remote" {
		t.Fatalf("expected remote to override local, got %q", resp)
	}
}

func TestReload_UnchangedRoutePreservesHandler(t *testing.T) {
	db := setupTestDB(t)
	r := New()

	var buildCount int32
	r.RegisterTransport("http", func(endpoint string, config json.RawMessage) (Handler, func(), error) {
		atomic.AddInt32(&buildCount, 1)
		h := func(ctx context.Context, payload []byte) ([]byte, error) {
			return []byte("ok"), nil
		}
		return h, nil, nil
	})

	_, err := db.Exec(`INSERT INTO routes (service_name, strategy, endpoint) VALUES ('svc', 'http', 'http://10.0.0.1')`)
	if err != nil {
		t.Fatal(err)
	}

	// First reload builds the handler.
	if err := r.Reload(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if c := atomic.LoadInt32(&buildCount); c != 1 {
		t.Fatalf("expected 1 build, got %d", c)
	}

	// Second reload with same config should NOT rebuild.
	if err := r.Reload(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if c := atomic.LoadInt32(&buildCount); c != 1 {
		t.Fatalf("expected still 1 build after unchanged reload, got %d", c)
	}
}

func TestReload_ChangedRouteRebuildsHandler(t *testing.T) {
	db := setupTestDB(t)
	r := New()

	var buildCount int32
	closeCalled := false
	r.RegisterTransport("http", func(endpoint string, config json.RawMessage) (Handler, func(), error) {
		atomic.AddInt32(&buildCount, 1)
		h := func(ctx context.Context, payload []byte) ([]byte, error) {
			return []byte(endpoint), nil
		}
		cl := func() { closeCalled = true }
		return h, cl, nil
	})

	_, err := db.Exec(`INSERT INTO routes (service_name, strategy, endpoint) VALUES ('svc', 'http', 'http://old')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Reload(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	// Update endpoint.
	_, err = db.Exec(`UPDATE routes SET endpoint='http://new' WHERE service_name='svc'`)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Reload(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	if c := atomic.LoadInt32(&buildCount); c != 2 {
		t.Fatalf("expected 2 builds after endpoint change, got %d", c)
	}
	if !closeCalled {
		t.Fatal("old handler close function not called")
	}

	resp, err := r.Call(context.Background(), "svc", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp) != "http://new" {
		t.Fatalf("expected new endpoint, got %q", resp)
	}
}

func TestReload_RemovedRouteClosesHandler(t *testing.T) {
	db := setupTestDB(t)
	r := New()

	closeCalled := false
	r.RegisterTransport("http", func(endpoint string, config json.RawMessage) (Handler, func(), error) {
		h := func(ctx context.Context, payload []byte) ([]byte, error) {
			return nil, nil
		}
		return h, func() { closeCalled = true }, nil
	})

	_, err := db.Exec(`INSERT INTO routes (service_name, strategy, endpoint) VALUES ('svc', 'http', 'http://x')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Reload(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	// Remove the route.
	_, err = db.Exec(`DELETE FROM routes WHERE service_name='svc'`)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Reload(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	if !closeCalled {
		t.Fatal("close not called for removed route")
	}
}

func TestReload_NoFactoryWarns(t *testing.T) {
	db := setupTestDB(t)
	r := New()

	// No factory registered for "dbsync".
	_, err := db.Exec(`INSERT INTO routes (service_name, strategy, endpoint) VALUES ('svc', 'dbsync', 'quic://x')`)
	if err != nil {
		t.Fatal(err)
	}

	// Should not error — just skip.
	if err := r.Reload(context.Background(), db); err != nil {
		t.Fatal(err)
	}

	// Service should not be routable.
	_, err = r.Call(context.Background(), "svc", nil)
	if err == nil {
		t.Fatal("expected error for service with no factory")
	}
}

func TestClose(t *testing.T) {
	r := New()

	closeCalled := false
	r.remoteEntries["svc"] = remoteEntry{
		handler: func(ctx context.Context, payload []byte) ([]byte, error) { return nil, nil },
		close:   func() { closeCalled = true },
	}

	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if !closeCalled {
		t.Fatal("close not called")
	}
	if len(r.remoteEntries) != 0 {
		t.Fatal("entries not cleared")
	}
}

func TestCircuitBreaker_OpensAndRecovers(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }

	cb := NewCircuitBreaker(
		WithBreakerThreshold(3),
		WithBreakerResetTimeout(100*time.Millisecond),
		WithBreakerHalfOpenMax(1),
		WithBreakerClock(clock),
	)

	if cb.State() != BreakerClosed {
		t.Fatal("expected closed")
	}

	// Record 3 failures to open.
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}
	if cb.State() != BreakerOpen {
		t.Fatal("expected open after 3 failures")
	}

	if cb.Allow() {
		t.Fatal("should not allow when open")
	}

	// Advance time past reset timeout.
	now = now.Add(200 * time.Millisecond)
	if cb.State() != BreakerHalfOpen {
		t.Fatal("expected half-open after reset timeout")
	}
	if !cb.Allow() {
		t.Fatal("should allow in half-open")
	}

	// One success closes it.
	cb.RecordSuccess()
	if cb.State() != BreakerClosed {
		t.Fatal("expected closed after success in half-open")
	}
}

func TestCircuitBreaker_HalfOpenFailureReopens(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }

	cb := NewCircuitBreaker(
		WithBreakerThreshold(1),
		WithBreakerResetTimeout(50*time.Millisecond),
		WithBreakerClock(clock),
	)

	cb.RecordFailure()
	if cb.State() != BreakerOpen {
		t.Fatal("expected open")
	}

	now = now.Add(100 * time.Millisecond)
	if cb.State() != BreakerHalfOpen {
		t.Fatal("expected half-open")
	}

	cb.RecordFailure()
	if cb.State() != BreakerOpen {
		t.Fatal("expected re-open after failure in half-open")
	}
}

func TestWithCircuitBreaker_Middleware(t *testing.T) {
	cb := NewCircuitBreaker(WithBreakerThreshold(1))
	service := "test"

	base := func(ctx context.Context, payload []byte) ([]byte, error) {
		return nil, errors.New("fail")
	}

	wrapped := WithCircuitBreaker(cb, service)(base)

	// First call fails, records failure, trips breaker.
	_, err := wrapped(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error")
	}

	// Second call should be rejected by circuit breaker.
	_, err = wrapped(context.Background(), nil)
	var eco *ErrCircuitOpen
	if !errors.As(err, &eco) {
		t.Fatalf("expected ErrCircuitOpen, got %T: %v", err, err)
	}
}

func TestWithRetry(t *testing.T) {
	attempts := 0
	base := func(ctx context.Context, payload []byte) ([]byte, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("transient")
		}
		return []byte("ok"), nil
	}

	wrapped := WithRetry(3, 1*time.Millisecond, nil)(base)
	resp, err := wrapped(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	if string(resp) != "ok" {
		t.Fatalf("got %q", resp)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestWithRetry_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	attempts := 0
	base := func(ctx context.Context, payload []byte) ([]byte, error) {
		attempts++
		cancel() // cancel after first attempt
		return nil, errors.New("fail")
	}

	wrapped := WithRetry(5, 1*time.Millisecond, nil)(base)
	_, err := wrapped(ctx, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt (context cancelled), got %d", attempts)
	}
}

func TestWithFallback(t *testing.T) {
	local := func(ctx context.Context, payload []byte) ([]byte, error) {
		return []byte("local"), nil
	}

	remote := func(ctx context.Context, payload []byte) ([]byte, error) {
		return nil, errors.New("remote down")
	}

	wrapped := WithFallback(local, "svc", slog.Default())(remote)
	resp, err := wrapped(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(resp) != "local" {
		t.Fatalf("expected fallback to local, got %q", resp)
	}
}

func TestWithFallback_NoFallbackOnContextCancel(t *testing.T) {
	localCalled := false
	local := func(ctx context.Context, payload []byte) ([]byte, error) {
		localCalled = true
		return nil, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	remote := func(ctx context.Context, payload []byte) ([]byte, error) {
		return nil, ctx.Err()
	}

	wrapped := WithFallback(local, "svc", nil)(remote)
	_, err := wrapped(ctx, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if localCalled {
		t.Fatal("local should not be called on context cancellation")
	}
}

func TestChain(t *testing.T) {
	var order []string

	mw1 := func(next Handler) Handler {
		return func(ctx context.Context, payload []byte) ([]byte, error) {
			order = append(order, "mw1-before")
			resp, err := next(ctx, payload)
			order = append(order, "mw1-after")
			return resp, err
		}
	}
	mw2 := func(next Handler) Handler {
		return func(ctx context.Context, payload []byte) ([]byte, error) {
			order = append(order, "mw2-before")
			resp, err := next(ctx, payload)
			order = append(order, "mw2-after")
			return resp, err
		}
	}

	base := func(ctx context.Context, payload []byte) ([]byte, error) {
		order = append(order, "handler")
		return nil, nil
	}

	wrapped := Chain(mw1, mw2)(base)
	wrapped(context.Background(), nil)

	expected := []string{"mw1-before", "mw2-before", "handler", "mw2-after", "mw1-after"}
	if len(order) != len(expected) {
		t.Fatalf("got %v, want %v", order, expected)
	}
	for i, v := range expected {
		if order[i] != v {
			t.Fatalf("at index %d: got %q, want %q", i, order[i], v)
		}
	}
}

func TestRecovery(t *testing.T) {
	base := func(ctx context.Context, payload []byte) ([]byte, error) {
		panic("boom")
	}

	wrapped := Recovery(slog.Default())(base)
	_, err := wrapped(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error from recovered panic")
	}
	var ep *ErrPanic
	if !errors.As(err, &ep) {
		t.Fatalf("expected ErrPanic, got %T: %v", err, err)
	}
}

func TestHTTPFactory_CreatesHandler(t *testing.T) {
	f := HTTPFactory()
	cfg := json.RawMessage(`{"timeout_ms": 5000, "content_type": "application/json"}`)
	// Use an external URL — SSRF guard rejects private/loopback addresses.
	h, closeFn, err := f("https://example.com/api", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if h == nil {
		t.Fatal("handler is nil")
	}
	if closeFn == nil {
		t.Fatal("close function is nil")
	}
	closeFn()
}

func TestHTTPFactory_RejectsPrivateURL(t *testing.T) {
	f := HTTPFactory()
	cfg := json.RawMessage(`{}`)
	_, _, err := f("http://127.0.0.1:8080", cfg)
	if err == nil {
		t.Fatal("expected SSRF error for loopback URL")
	}
	_, _, err = f("http://10.0.0.1:8080", cfg)
	if err == nil {
		t.Fatal("expected SSRF error for private URL")
	}
}

func TestFingerprint(t *testing.T) {
	r1 := route{Strategy: "http", Endpoint: "http://a", Config: json.RawMessage(`{}`)}
	r2 := route{Strategy: "http", Endpoint: "http://a", Config: json.RawMessage(`{}`)}
	r3 := route{Strategy: "http", Endpoint: "http://b", Config: json.RawMessage(`{}`)}

	if r1.fingerprint() != r2.fingerprint() {
		t.Fatal("same routes should have same fingerprint")
	}
	if r1.fingerprint() == r3.fingerprint() {
		t.Fatal("different routes should have different fingerprint")
	}
}

func TestWatch_DetectsChanges(t *testing.T) {
	// PRAGMA data_version only changes when a *different* connection writes.
	// Use a temp file database with two separate sql.DB handles.
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/watch_test.db"

	// Writer connection — used to insert routes.
	writerDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writerDB.Close() })

	if err := Init(writerDB); err != nil {
		t.Fatal(err)
	}

	// Reader connection — used by the watcher.
	readerDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { readerDB.Close() })

	r := New()
	var callCount int32
	r.RegisterTransport("http", func(endpoint string, config json.RawMessage) (Handler, func(), error) {
		atomic.AddInt32(&callCount, 1)
		h := func(ctx context.Context, payload []byte) ([]byte, error) { return nil, nil }
		return h, nil, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go r.Watch(ctx, readerDB, 50*time.Millisecond)

	// Wait for initial load.
	time.Sleep(100 * time.Millisecond)

	// Insert via the writer connection — triggers data_version change on reader.
	_, err = writerDB.Exec(`INSERT INTO routes (service_name, strategy, endpoint) VALUES ('svc', 'http', 'http://x')`)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the watcher to pick it up.
	time.Sleep(300 * time.Millisecond)

	if c := atomic.LoadInt32(&callCount); c < 1 {
		t.Fatalf("expected factory to be called after route insert, got %d calls", c)
	}
}

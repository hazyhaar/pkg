package circuitbreaker

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestNew_DefaultsClosed(t *testing.T) {
	b := New("test")
	if b.State() != Closed {
		t.Fatalf("new breaker should be Closed, got %s", b.State())
	}
}

func TestName(t *testing.T) {
	b := New("my-service")
	if b.Name() != "my-service" {
		t.Fatalf("Name() = %q, want %q", b.Name(), "my-service")
	}
}

func TestAllow_Closed(t *testing.T) {
	b := New("test")
	if !b.Allow() {
		t.Fatal("closed breaker should allow calls")
	}
}

func TestTripsToOpen_AfterThreshold(t *testing.T) {
	b := New("test", WithThreshold(3))

	for i := 0; i < 3; i++ {
		b.RecordFailure()
	}

	if b.State() != Open {
		t.Fatalf("expected Open after %d failures, got %s", 3, b.State())
	}
	if b.Allow() {
		t.Fatal("open breaker should not allow calls")
	}
}

func TestTripsToOpen_NotBeforeThreshold(t *testing.T) {
	b := New("test", WithThreshold(3))

	b.RecordFailure()
	b.RecordFailure()

	if b.State() != Closed {
		t.Fatalf("expected Closed before threshold, got %s", b.State())
	}
}

func TestSuccessResetsFailureCount(t *testing.T) {
	b := New("test", WithThreshold(3))

	b.RecordFailure()
	b.RecordFailure()
	b.RecordSuccess()
	b.RecordFailure()
	b.RecordFailure()

	if b.State() != Closed {
		t.Fatalf("success should reset failure count, got %s", b.State())
	}
}

func TestOpenToHalfOpen_AfterTimeout(t *testing.T) {
	now := time.Now()
	b := New("test",
		WithThreshold(1),
		WithResetTimeout(10*time.Second),
		WithClock(func() time.Time { return now }),
	)

	b.RecordFailure()
	if b.State() != Open {
		t.Fatalf("expected Open, got %s", b.State())
	}

	// Advance clock past reset timeout.
	now = now.Add(11 * time.Second)
	if b.State() != HalfOpen {
		t.Fatalf("expected HalfOpen after timeout, got %s", b.State())
	}
}

func TestHalfOpen_SuccessCloses(t *testing.T) {
	now := time.Now()
	b := New("test",
		WithThreshold(1),
		WithResetTimeout(10*time.Second),
		WithHalfOpenMax(2),
		WithClock(func() time.Time { return now }),
	)

	b.RecordFailure()
	now = now.Add(11 * time.Second) // Move to half-open.

	b.RecordSuccess()
	if b.State() != HalfOpen {
		t.Fatalf("one success should not close, got %s", b.State())
	}

	b.RecordSuccess()
	if b.State() != Closed {
		t.Fatalf("two successes should close, got %s", b.State())
	}
}

func TestHalfOpen_FailureReopens(t *testing.T) {
	now := time.Now()
	b := New("test",
		WithThreshold(1),
		WithResetTimeout(10*time.Second),
		WithClock(func() time.Time { return now }),
	)

	b.RecordFailure()
	now = now.Add(11 * time.Second) // Move to half-open.

	b.State() // Trigger transition.
	b.RecordFailure()
	if b.State() != Open {
		t.Fatalf("failure in half-open should reopen, got %s", b.State())
	}
}

func TestReset(t *testing.T) {
	b := New("test", WithThreshold(1))
	b.RecordFailure()
	if b.State() != Open {
		t.Fatal("expected Open")
	}
	b.Reset()
	if b.State() != Closed {
		t.Fatalf("Reset should return to Closed, got %s", b.State())
	}
}

func TestExecute_Success(t *testing.T) {
	b := New("test")
	err := b.Execute(context.Background(), func() error {
		return nil
	})
	if err != nil {
		t.Fatalf("Execute success should return nil, got: %v", err)
	}
}

func TestExecute_Failure(t *testing.T) {
	b := New("test", WithThreshold(1))
	testErr := errors.New("test error")

	err := b.Execute(context.Background(), func() error {
		return testErr
	})
	if !errors.Is(err, testErr) {
		t.Fatalf("Execute should return original error, got: %v", err)
	}
	if b.State() != Open {
		t.Fatalf("breaker should be open after failure, got %s", b.State())
	}
}

func TestExecute_RejectsWhenOpen(t *testing.T) {
	b := New("test", WithThreshold(1))
	b.RecordFailure()

	err := b.Execute(context.Background(), func() error {
		t.Fatal("function should not be called when open")
		return nil
	})

	var openErr *ErrOpen
	if !errors.As(err, &openErr) {
		t.Fatalf("expected ErrOpen, got: %v", err)
	}
	if openErr.Name != "test" {
		t.Fatalf("ErrOpen.Name = %q, want %q", openErr.Name, "test")
	}
}

func TestStateString(t *testing.T) {
	tests := []struct {
		s    State
		want string
	}{
		{Closed, "closed"},
		{Open, "open"},
		{HalfOpen, "half-open"},
		{State(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

// --- SQLite persistence tests ---

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

func TestSQLiteStore_PersistsState(t *testing.T) {
	db := setupTestDB(t)

	b := New("svc", WithThreshold(2), WithSQLite(db))
	b.RecordFailure()
	b.RecordFailure()

	if b.State() != Open {
		t.Fatalf("expected Open, got %s", b.State())
	}

	// Load in a new breaker from the same DB.
	b2 := New("svc", WithSQLite(db))
	if b2.State() != Open {
		t.Fatalf("persisted state should be Open, got %s", b2.State())
	}
}

func TestSQLiteStore_ResetPersists(t *testing.T) {
	db := setupTestDB(t)

	b := New("svc", WithThreshold(1), WithSQLite(db))
	b.RecordFailure()
	b.Reset()

	b2 := New("svc", WithSQLite(db))
	if b2.State() != Closed {
		t.Fatalf("reset should persist as Closed, got %s", b2.State())
	}
}

func TestSQLiteStore_MultipleBreakers(t *testing.T) {
	db := setupTestDB(t)

	b1 := New("svc-a", WithThreshold(1), WithSQLite(db))
	b2 := New("svc-b", WithThreshold(1), WithSQLite(db))

	b1.RecordFailure()

	// b1 should be open, b2 should be closed.
	b1Loaded := New("svc-a", WithSQLite(db))
	b2Loaded := New("svc-b", WithSQLite(db))

	if b1Loaded.State() != Open {
		t.Fatalf("svc-a should be Open, got %s", b1Loaded.State())
	}
	if b2Loaded.State() != Closed {
		t.Fatalf("svc-b should be Closed, got %s", b2Loaded.State())
	}
	_ = b2 // used above implicitly
}

func TestSQLiteStore_InitIdempotent(t *testing.T) {
	db := setupTestDB(t)
	s := &sqliteStore{db: db}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("second Init should be idempotent: %v", err)
	}
}

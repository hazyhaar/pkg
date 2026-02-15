package watch

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite" // register "sqlite" driver
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	// Force single connection so PRAGMA changes are visible to all callers.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func setUserVersion(t *testing.T, db *sql.DB, v int) {
	t.Helper()
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", v)); err != nil {
		t.Fatal(err)
	}
}

func TestPragmaDataVersion(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	v, err := PragmaDataVersion(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if v < 0 {
		t.Fatalf("expected non-negative version, got %d", v)
	}
}

func TestPragmaUserVersion(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	v, err := PragmaUserVersion(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if v != 0 {
		t.Fatalf("expected 0, got %d", v)
	}

	if _, err := db.Exec("PRAGMA user_version = 42"); err != nil {
		t.Fatal(err)
	}
	v, err = PragmaUserVersion(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}
}

func TestMaxColumnDetector(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if _, err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, ts INTEGER)"); err != nil {
		t.Fatal(err)
	}

	det := MaxColumnDetector("items", "ts")
	v, err := det(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if v != 0 {
		t.Fatalf("expected 0 for empty table, got %d", v)
	}

	if _, err := db.Exec("INSERT INTO items (ts) VALUES (100)"); err != nil {
		t.Fatal(err)
	}
	v, err = det(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if v != 100 {
		t.Fatalf("expected 100, got %d", v)
	}
}

func TestOnChange_FiresOnVersionChange(t *testing.T) {
	db := testDB(t)

	// Use user_version as detector so we can control it.
	var reloadCount atomic.Int32
	w := New(db, Options{
		Interval: 20 * time.Millisecond,
		Detector: PragmaUserVersion,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go w.OnChange(ctx, func() error {
		reloadCount.Add(1)
		return nil
	})

	// Wait for initial version to be read.
	time.Sleep(50 * time.Millisecond)

	// Bump version → should trigger reload.
	setUserVersion(t, db, 1)
	time.Sleep(80 * time.Millisecond)

	if got := reloadCount.Load(); got != 1 {
		t.Fatalf("expected 1 reload, got %d", got)
	}

	// Bump again.
	setUserVersion(t, db, 2)
	time.Sleep(80 * time.Millisecond)

	if got := reloadCount.Load(); got != 2 {
		t.Fatalf("expected 2 reloads, got %d", got)
	}

	// No bump → no extra reload.
	time.Sleep(80 * time.Millisecond)
	if got := reloadCount.Load(); got != 2 {
		t.Fatalf("expected still 2, got %d", got)
	}
}

func TestOnChange_Debounce(t *testing.T) {
	db := testDB(t)

	var reloadCount atomic.Int32
	w := New(db, Options{
		Interval: 20 * time.Millisecond,
		Debounce: 100 * time.Millisecond,
		Detector: PragmaUserVersion,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go w.OnChange(ctx, func() error {
		reloadCount.Add(1)
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	// Rapid-fire 5 version bumps within 100ms window.
	for i := 1; i <= 5; i++ {
		setUserVersion(t, db, i)
		time.Sleep(15 * time.Millisecond)
	}

	// Should NOT have fired yet (debounce window still open).
	if got := reloadCount.Load(); got != 0 {
		t.Fatalf("expected 0 reloads during debounce, got %d", got)
	}

	// Wait for debounce to settle.
	time.Sleep(200 * time.Millisecond)

	if got := reloadCount.Load(); got != 1 {
		t.Fatalf("expected exactly 1 debounced reload, got %d", got)
	}
}

func TestOnChange_ErrorDoesNotAdvanceVersion(t *testing.T) {
	db := testDB(t)

	var callCount atomic.Int32
	w := New(db, Options{
		Interval: 20 * time.Millisecond,
		Detector: PragmaUserVersion,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go w.OnChange(ctx, func() error {
		n := callCount.Add(1)
		if n == 1 {
			return context.DeadlineExceeded // simulate failure
		}
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	setUserVersion(t, db, 1)

	// First attempt: fail. Second attempt (next poll): succeed.
	time.Sleep(120 * time.Millisecond)

	if got := callCount.Load(); got < 2 {
		t.Fatalf("expected at least 2 calls (1 fail + 1 success), got %d", got)
	}

	// Version should now be advanced.
	if v := w.Version(); v != 1 {
		t.Fatalf("expected version 1, got %d", v)
	}
}

func TestWaitForVersion(t *testing.T) {
	db := testDB(t)

	w := New(db, Options{
		Interval: 20 * time.Millisecond,
		Detector: PragmaUserVersion,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go w.OnChange(ctx, func() error { return nil })

	time.Sleep(50 * time.Millisecond)

	// Bump version in background after a short delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		db.Exec(fmt.Sprintf("PRAGMA user_version = %d", 10))
	}()

	if err := w.WaitForVersion(ctx, 10); err != nil {
		t.Fatalf("WaitForVersion: %v", err)
	}

	if v := w.Version(); v < 10 {
		t.Fatalf("expected version >= 10, got %d", v)
	}
}

func TestWaitForVersion_Timeout(t *testing.T) {
	db := testDB(t)

	w := New(db, Options{
		Interval: 20 * time.Millisecond,
		Detector: PragmaUserVersion,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go w.OnChange(ctx, func() error { return nil })

	time.Sleep(50 * time.Millisecond)

	// Short timeout — version 99 will never appear.
	waitCtx, waitCancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer waitCancel()

	err := w.WaitForVersion(waitCtx, 99)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestStats(t *testing.T) {
	db := testDB(t)

	w := New(db, Options{
		Interval: 20 * time.Millisecond,
		Detector: PragmaUserVersion,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go w.OnChange(ctx, func() error { return nil })
	time.Sleep(50 * time.Millisecond)

	setUserVersion(t, db, 1)
	time.Sleep(80 * time.Millisecond)

	s := w.Stats()
	if s.Checks == 0 {
		t.Fatal("expected checks > 0")
	}
	if s.ChangesDetected == 0 {
		t.Fatal("expected changes > 0")
	}
	if s.Reloads == 0 {
		t.Fatal("expected reloads > 0")
	}
}

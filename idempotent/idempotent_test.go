package idempotent

import (
	"context"
	"database/sql"
	"errors"
	"strings"
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

func setupTestGuard(t *testing.T) (*sql.DB, *Guard) {
	t.Helper()
	db := setupTestDB(t)
	g := New(db)
	if err := g.Init(); err != nil {
		t.Fatal(err)
	}
	return db, g
}

func TestInit_CreatesTable(t *testing.T) {
	db := setupTestDB(t)
	g := New(db)
	if err := g.Init(); err != nil {
		t.Fatal(err)
	}
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='idempotent_log'").Scan(&name)
	if err != nil {
		t.Fatal("idempotent_log table not created")
	}
}

func TestInit_Idempotent(t *testing.T) {
	db := setupTestDB(t)
	g := New(db)
	if err := g.Init(); err != nil {
		t.Fatal(err)
	}
	if err := g.Init(); err != nil {
		t.Fatalf("second Init should be idempotent: %v", err)
	}
}

func TestOnce_ExecutesOnce(t *testing.T) {
	_, g := setupTestGuard(t)
	ctx := context.Background()

	callCount := 0
	fn := func() ([]byte, error) {
		callCount++
		return []byte("result"), nil
	}

	// First call.
	result, err := g.Once(ctx, "key1", fn)
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != "result" {
		t.Fatalf("got %q, want %q", result, "result")
	}
	if callCount != 1 {
		t.Fatalf("fn called %d times, want 1", callCount)
	}

	// Second call — should not execute fn again.
	result2, err := g.Once(ctx, "key1", fn)
	if err != nil {
		t.Fatal(err)
	}
	if string(result2) != "result" {
		t.Fatalf("cached result = %q, want %q", result2, "result")
	}
	if callCount != 1 {
		t.Fatalf("fn called %d times on second call, want 1", callCount)
	}
}

func TestOnce_DifferentKeys(t *testing.T) {
	_, g := setupTestGuard(t)
	ctx := context.Background()

	count := 0
	fn := func() ([]byte, error) {
		count++
		return []byte("ok"), nil
	}

	g.Once(ctx, "key-a", fn)
	g.Once(ctx, "key-b", fn)
	g.Once(ctx, "key-a", fn) // duplicate

	if count != 2 {
		t.Fatalf("fn called %d times, want 2 (one per unique key)", count)
	}
}

func TestOnce_StoresError(t *testing.T) {
	_, g := setupTestGuard(t)
	ctx := context.Background()

	testErr := errors.New("something failed")
	fn := func() ([]byte, error) {
		return nil, testErr
	}

	_, err := g.Once(ctx, "fail-key", fn)
	if err == nil {
		t.Fatal("expected error")
	}

	// Second call should return the stored error without re-executing.
	callCount := 0
	_, err = g.Once(ctx, "fail-key", func() ([]byte, error) {
		callCount++
		return nil, errors.New("different error")
	})
	if err == nil {
		t.Fatal("expected stored error on second call")
	}
	if !strings.Contains(err.Error(), "something failed") {
		t.Fatalf("expected original error message, got: %v", err)
	}
	if callCount != 0 {
		t.Fatal("fn should not be called on duplicate key")
	}
}

func TestSeen(t *testing.T) {
	_, g := setupTestGuard(t)
	ctx := context.Background()

	seen, err := g.Seen(ctx, "absent")
	if err != nil {
		t.Fatal(err)
	}
	if seen {
		t.Fatal("key should not be seen")
	}

	g.Once(ctx, "present", func() ([]byte, error) {
		return []byte("ok"), nil
	})

	seen, err = g.Seen(ctx, "present")
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("key should be seen after Once")
	}
}

func TestPrune(t *testing.T) {
	db, g := setupTestGuard(t)
	ctx := context.Background()

	// Insert entries with old timestamps.
	g.Once(ctx, "old-key", func() ([]byte, error) { return []byte("old"), nil })

	// Backdate the entry.
	db.Exec("UPDATE idempotent_log SET created_at = ? WHERE original_key = 'old-key'",
		time.Now().Add(-48*time.Hour).Unix())

	g.Once(ctx, "new-key", func() ([]byte, error) { return []byte("new"), nil })

	deleted, err := g.Prune(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}

	// Old key should now be re-executable.
	seen, _ := g.Seen(ctx, "old-key")
	if seen {
		t.Fatal("pruned key should not be seen")
	}

	// New key should still exist.
	seen, _ = g.Seen(ctx, "new-key")
	if !seen {
		t.Fatal("new key should still be seen")
	}
}

func TestDelete(t *testing.T) {
	_, g := setupTestGuard(t)
	ctx := context.Background()

	g.Once(ctx, "del-key", func() ([]byte, error) { return []byte("ok"), nil })

	if err := g.Delete(ctx, "del-key"); err != nil {
		t.Fatal(err)
	}

	seen, _ := g.Seen(ctx, "del-key")
	if seen {
		t.Fatal("deleted key should not be seen")
	}

	// Should be re-executable now.
	callCount := 0
	g.Once(ctx, "del-key", func() ([]byte, error) {
		callCount++
		return []byte("new"), nil
	})
	if callCount != 1 {
		t.Fatal("fn should be called after delete")
	}
}

func TestCount(t *testing.T) {
	_, g := setupTestGuard(t)
	ctx := context.Background()

	count, err := g.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("empty table count = %d, want 0", count)
	}

	g.Once(ctx, "a", func() ([]byte, error) { return nil, nil })
	g.Once(ctx, "b", func() ([]byte, error) { return nil, nil })

	count, err = g.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

func TestOnce_NilResult(t *testing.T) {
	_, g := setupTestGuard(t)
	ctx := context.Background()

	result, err := g.Once(ctx, "nil-key", func() ([]byte, error) {
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("expected nil result, got %q", result)
	}

	// Second call should return cached nil.
	result2, err := g.Once(ctx, "nil-key", func() ([]byte, error) {
		t.Fatal("should not be called")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result2 != nil {
		t.Fatalf("expected nil cached result, got %q", result2)
	}
}

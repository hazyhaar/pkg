package sqlitedb

import (
	"context"
	"database/sql"
	"testing"
)

func TestOpen(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Verify WAL mode.
	var mode string
	db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	// In-memory databases may report "memory" instead of "wal".
	if mode != "wal" && mode != "memory" {
		t.Fatalf("journal_mode: got %q", mode)
	}

	// Verify foreign keys.
	var fk int
	db.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	if fk != 1 {
		t.Fatalf("foreign_keys: got %d, want 1", fk)
	}
}

func TestExecuteWithRetry(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, val TEXT)")

	err = ExecuteWithRetry(db, 3, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO test (id, val) VALUES (1, 'hello')")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	var val string
	db.QueryRow("SELECT val FROM test WHERE id = 1").Scan(&val)
	if val != "hello" {
		t.Fatalf("got %q, want hello", val)
	}
}

func TestMigrate(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	migrations := []Migration{
		{Version: 1, Description: "create users", SQL: "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)"},
		{Version: 2, Description: "add email", SQL: "ALTER TABLE users ADD COLUMN email TEXT"},
	}

	// Apply all.
	if err := Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}

	// Verify table exists with both columns.
	_, err = db.Exec("INSERT INTO users (id, name, email) VALUES (1, 'alice', 'a@b.c')")
	if err != nil {
		t.Fatalf("insert after migration: %v", err)
	}

	// Current version should be 2.
	ver, err := CurrentVersion(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if ver != 2 {
		t.Fatalf("version: got %d, want 2", ver)
	}

	// Re-apply should be idempotent.
	if err := Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
}

func TestMigrate_OutOfOrder(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	// Apply version 2 first (reversed order).
	migrations := []Migration{
		{Version: 2, Description: "second", SQL: "CREATE TABLE t2 (id INTEGER)"},
		{Version: 1, Description: "first", SQL: "CREATE TABLE t1 (id INTEGER)"},
	}

	if err := Migrate(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}

	// Both tables should exist (sorted by version internally).
	for _, table := range []string{"t1", "t2"} {
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
		if err != nil || count != 1 {
			t.Fatalf("table %s not found", table)
		}
	}
}

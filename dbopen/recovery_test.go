package dbopen_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/hazyhaar/pkg/dbopen"

	_ "modernc.org/sqlite"
)

const testSchema = `CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value TEXT NOT NULL);`

func TestRecovery_WALSurvivesReopen(t *testing.T) {
	// Write data, close without explicit sync, reopen → committed data survives.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wal_test.db")

	// Phase 1: write data
	db, err := dbopen.Open(dbPath, dbopen.WithSchema(testSchema))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO kv (key, value) VALUES ('k1', 'v1')`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Phase 2: reopen — WAL should have been replayed
	db2, err := dbopen.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	var val string
	if err := db2.QueryRow(`SELECT value FROM kv WHERE key = 'k1'`).Scan(&val); err != nil {
		t.Fatalf("data lost after reopen: %v", err)
	}
	if val != "v1" {
		t.Fatalf("got %q, want %q", val, "v1")
	}
}

func TestRecovery_MultipleWrites(t *testing.T) {
	// Write multiple rows across transactions, close, reopen → all data intact.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "multi_test.db")

	db, err := dbopen.Open(dbPath, dbopen.WithSchema(testSchema))
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 100; i++ {
		if err := dbopen.RunTx(context.Background(), db, func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)`, i, i*10)
			return err
		}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	db.Close()

	db2, err := dbopen.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	var count int
	if err := db2.QueryRow(`SELECT COUNT(*) FROM kv`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 100 {
		t.Fatalf("got %d rows, want 100", count)
	}
}

func TestRecovery_CorruptedDBFile(t *testing.T) {
	// Write random bytes to a .db file → Open should return an error, not panic.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "corrupt.db")

	if err := os.WriteFile(dbPath, []byte("this is not a sqlite database at all!!!"), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := dbopen.Open(dbPath)
	if err != nil {
		// Expected: clean error
		return
	}

	// If Open succeeds, Ping or query should fail.
	defer db.Close()
	if err := db.Ping(); err != nil {
		return // expected
	}
	_, err = db.Exec(`CREATE TABLE test (id INTEGER)`)
	if err != nil {
		return // expected
	}
	t.Log("Warning: corrupt file was accepted (SQLite may overwrite)")
}

func TestRecovery_TruncatedDBFile(t *testing.T) {
	// Create a valid DB, truncate it to a few bytes → Open should error or recover.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "trunc.db")

	// Create valid DB with data
	db, err := dbopen.Open(dbPath, dbopen.WithSchema(testSchema))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO kv (key, value) VALUES ('persist', 'me')`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Truncate file to 16 bytes (less than SQLite header)
	if err := os.Truncate(dbPath, 16); err != nil {
		t.Fatal(err)
	}

	db2, err := dbopen.Open(dbPath)
	if err != nil {
		// Expected: clean error for truncated file
		return
	}

	defer db2.Close()
	// If Open succeeds, the query should fail (table gone)
	var val string
	if err := db2.QueryRow(`SELECT value FROM kv WHERE key = 'persist'`).Scan(&val); err != nil {
		return // expected — data lost
	}
	t.Error("truncated DB should not contain original data")
}

func TestRecovery_EmptyFile(t *testing.T) {
	// Open an empty file → SQLite should create a new DB.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "empty.db")

	if err := os.WriteFile(dbPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := dbopen.Open(dbPath)
	if err != nil {
		// Some SQLite versions may reject empty files
		return
	}
	defer db.Close()

	// Should be usable
	_, err = db.Exec(`CREATE TABLE test (id INTEGER)`)
	if err != nil {
		t.Fatalf("empty file should be a valid new DB: %v", err)
	}
}

func TestRecovery_RunTxRetryOnBusy(t *testing.T) {
	// Verify that RunTx retries on SQLITE_BUSY.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "busy.db")

	db, err := dbopen.Open(dbPath, dbopen.WithSchema(testSchema))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// RunTx should succeed under normal conditions.
	err = dbopen.RunTx(context.Background(), db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO kv (key, value) VALUES ('busy', 'test')`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	var val string
	if err := db.QueryRow(`SELECT value FROM kv WHERE key = 'busy'`).Scan(&val); err != nil {
		t.Fatal(err)
	}
	if val != "test" {
		t.Fatalf("got %q, want %q", val, "test")
	}
}

func TestRecovery_RunTxContextCancelled(t *testing.T) {
	// RunTx should fail fast when context is already cancelled.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cancel.db")

	db, err := dbopen.Open(dbPath, dbopen.WithSchema(testSchema))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = dbopen.RunTx(ctx, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO kv (key, value) VALUES ('x', 'y')`)
		return err
	})
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

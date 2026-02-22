package dbopen_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/pkg/dbopen"
)

func TestOpen(t *testing.T) {
	db := dbopen.OpenMemory(t)

	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	// :memory: may report "memory" instead of "wal" for journal_mode,
	// but the PRAGMA was still executed successfully.
	if journalMode != "wal" && journalMode != "memory" {
		t.Fatalf("journal_mode = %q, want wal or memory", journalMode)
	}

	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys = %d, want 1", fk)
	}

	var sync int
	if err := db.QueryRow("PRAGMA synchronous").Scan(&sync); err != nil {
		t.Fatal(err)
	}
	// synchronous NORMAL = 1
	if sync != 1 {
		t.Fatalf("synchronous = %d, want 1 (NORMAL)", sync)
	}

	var busyTimeout int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if busyTimeout != 10_000 {
		t.Fatalf("busy_timeout = %d, want 10000", busyTimeout)
	}
}

func TestOpenMemory(t *testing.T) {
	db := dbopen.OpenMemory(t)
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
}

func TestWithBusyTimeout(t *testing.T) {
	db := dbopen.OpenMemory(t, dbopen.WithBusyTimeout(5000))

	var bt int
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&bt); err != nil {
		t.Fatal(err)
	}
	if bt != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", bt)
	}
}

func TestWithoutForeignKeys(t *testing.T) {
	db := dbopen.OpenMemory(t, dbopen.WithoutForeignKeys())

	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 0 {
		t.Fatalf("foreign_keys = %d, want 0", fk)
	}
}

func TestWithCacheSize(t *testing.T) {
	db := dbopen.OpenMemory(t, dbopen.WithCacheSize(-64000))

	var cs int
	if err := db.QueryRow("PRAGMA cache_size").Scan(&cs); err != nil {
		t.Fatal(err)
	}
	if cs != -64000 {
		t.Fatalf("cache_size = %d, want -64000", cs)
	}
}

func TestWithSchema(t *testing.T) {
	schema := `CREATE TABLE test_table (id TEXT PRIMARY KEY, name TEXT);`
	db := dbopen.OpenMemory(t, dbopen.WithSchema(schema))

	_, err := db.Exec(`INSERT INTO test_table (id, name) VALUES ('1', 'hello')`)
	if err != nil {
		t.Fatalf("insert into schema-created table: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM test_table WHERE id = '1'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "hello" {
		t.Fatalf("name = %q, want hello", name)
	}
}

func TestWithSchemaFile(t *testing.T) {
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "schema.sql")
	if err := os.WriteFile(schemaPath, []byte(`CREATE TABLE file_test (id TEXT PRIMARY KEY);`), 0o644); err != nil {
		t.Fatal(err)
	}

	db := dbopen.OpenMemory(t, dbopen.WithSchemaFile(schemaPath))

	_, err := db.Exec(`INSERT INTO file_test (id) VALUES ('1')`)
	if err != nil {
		t.Fatalf("insert into schema-file table: %v", err)
	}
}

func TestWithMkdirAll(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sub", "deep", "test.db")

	db, err := dbopen.Open(dbPath, dbopen.WithMkdirAll())
	if err != nil {
		t.Fatalf("open with mkdirall: %v", err)
	}
	defer db.Close()

	if _, err := os.Stat(filepath.Dir(dbPath)); err != nil {
		t.Fatalf("directory not created: %v", err)
	}
}

func TestWithSynchronous(t *testing.T) {
	db := dbopen.OpenMemory(t, dbopen.WithSynchronous("FULL"))

	var sync int
	if err := db.QueryRow("PRAGMA synchronous").Scan(&sync); err != nil {
		t.Fatal(err)
	}
	// synchronous FULL = 2
	if sync != 2 {
		t.Fatalf("synchronous = %d, want 2 (FULL)", sync)
	}
}

func TestIsBusy(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("some other error"), false},
		{errors.New("SQLITE_BUSY"), true},
		{errors.New("database is locked"), true},
		{errors.New("database table is locked"), true},
		{errors.New("prefix: SQLITE_BUSY (5)"), true},
		{errors.New("something database is locked something"), true},
	}
	for _, tt := range tests {
		got := dbopen.IsBusy(tt.err)
		if got != tt.want {
			t.Errorf("IsBusy(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestRunTx(t *testing.T) {
	db := dbopen.OpenMemory(t, dbopen.WithSchema(`CREATE TABLE tx_test (id TEXT PRIMARY KEY, val TEXT)`))
	ctx := context.Background()

	err := dbopen.RunTx(ctx, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO tx_test (id, val) VALUES ('1', 'hello')`)
		return err
	})
	if err != nil {
		t.Fatalf("RunTx: %v", err)
	}

	var val string
	if err := db.QueryRow(`SELECT val FROM tx_test WHERE id = '1'`).Scan(&val); err != nil {
		t.Fatal(err)
	}
	if val != "hello" {
		t.Fatalf("val = %q, want hello", val)
	}
}

func TestRunTxRollback(t *testing.T) {
	db := dbopen.OpenMemory(t, dbopen.WithSchema(`CREATE TABLE tx_rb_test (id TEXT PRIMARY KEY)`))
	ctx := context.Background()

	sentinel := errors.New("rollback me")
	err := dbopen.RunTx(ctx, db, func(tx *sql.Tx) error {
		tx.Exec(`INSERT INTO tx_rb_test (id) VALUES ('1')`)
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("RunTx error = %v, want sentinel", err)
	}

	var count int
	db.QueryRow(`SELECT COUNT(*) FROM tx_rb_test`).Scan(&count)
	if count != 0 {
		t.Fatalf("count = %d, want 0 after rollback", count)
	}
}

func TestExec(t *testing.T) {
	db := dbopen.OpenMemory(t, dbopen.WithSchema(`CREATE TABLE exec_test (id TEXT PRIMARY KEY)`))
	ctx := context.Background()

	_, err := dbopen.Exec(ctx, db, `INSERT INTO exec_test (id) VALUES (?)`, "1")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	var count int
	db.QueryRow(`SELECT COUNT(*) FROM exec_test`).Scan(&count)
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestRunTxContextCancelled(t *testing.T) {
	db := dbopen.OpenMemory(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := dbopen.RunTx(ctx, db, func(tx *sql.Tx) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

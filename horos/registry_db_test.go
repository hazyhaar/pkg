//go:build cgo || sqlite

package horos

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRegistryInitDB(t *testing.T) {
	db := setupTestDB(t)
	r := NewRegistry()

	if err := r.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT count(*) FROM horos_formats").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 rows, got %d", count)
	}

	var name string
	if err := db.QueryRow("SELECT name FROM horos_formats WHERE id = ?", FormatJSON).Scan(&name); err != nil {
		t.Fatalf("query json: %v", err)
	}
	if name != "json" {
		t.Fatalf("expected json, got %s", name)
	}
}

func TestRegistrySyncToDB(t *testing.T) {
	db := setupTestDB(t)
	r := NewRegistry()
	if err := r.InitDB(db); err != nil {
		t.Fatalf("InitDB: %v", err)
	}

	_ = r.Register(FormatInfo{ID: 2, Name: "msgpack", MIME: "application/msgpack"})
	if err := r.SyncToDB(db); err != nil {
		t.Fatalf("SyncToDB: %v", err)
	}

	var count int
	if err := db.QueryRow("SELECT count(*) FROM horos_formats").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 rows after sync, got %d", count)
	}
}

func TestRegistryInitDBIdempotent(t *testing.T) {
	db := setupTestDB(t)
	r := NewRegistry()

	if err := r.InitDB(db); err != nil {
		t.Fatalf("InitDB 1: %v", err)
	}
	if err := r.InitDB(db); err != nil {
		t.Fatalf("InitDB 2: %v", err)
	}

	var count int
	_ = db.QueryRow("SELECT count(*) FROM horos_formats").Scan(&count)
	if count != 2 {
		t.Fatalf("expected 2 rows after double init, got %d", count)
	}
}

package trace

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupTraceDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestStore_Init(t *testing.T) {
	db := setupTraceDB(t)
	store := NewStore(db)
	defer store.Close()

	if err := store.Init(); err != nil {
		t.Fatal(err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='sql_traces'").Scan(&count)
	if count != 1 {
		t.Fatal("sql_traces table not created")
	}
}

func TestStore_RecordAsync_And_Close(t *testing.T) {
	db := setupTraceDB(t)
	store := NewStore(db)
	store.Init()

	for i := 0; i < 10; i++ {
		store.RecordAsync(&Entry{
			TraceID:    "trc_abc",
			Op:         "Query",
			Query:      "SELECT 1",
			DurationUs: 42,
			Timestamp:  time.Now().UnixMicro(),
		})
	}

	// Close flushes.
	store.Close()

	var count int
	db.QueryRow("SELECT COUNT(*) FROM sql_traces WHERE trace_id='trc_abc'").Scan(&count)
	if count != 10 {
		t.Fatalf("trace count: got %d, want 10", count)
	}
}

func TestStore_BatchFlush(t *testing.T) {
	db := setupTraceDB(t)
	store := NewStore(db)
	store.Init()

	// Fill beyond batch threshold (64).
	for i := 0; i < 100; i++ {
		store.RecordAsync(&Entry{
			Op:        "Exec",
			Query:     "INSERT INTO test VALUES (?)",
			Timestamp: time.Now().UnixMicro(),
		})
	}

	// Wait for batch flush.
	time.Sleep(200 * time.Millisecond)
	store.Close()

	var count int
	db.QueryRow("SELECT COUNT(*) FROM sql_traces").Scan(&count)
	if count != 100 {
		t.Fatalf("total traces: got %d, want 100", count)
	}
}

func TestStore_RecordAsync_ErrorField(t *testing.T) {
	db := setupTraceDB(t)
	store := NewStore(db)
	store.Init()

	store.RecordAsync(&Entry{
		Op:        "Exec",
		Query:     "bad sql",
		Error:     "syntax error",
		Timestamp: time.Now().UnixMicro(),
	})
	store.Close()

	var errMsg string
	db.QueryRow("SELECT error FROM sql_traces WHERE query='bad sql'").Scan(&errMsg)
	if errMsg != "syntax error" {
		t.Fatalf("error: got %q", errMsg)
	}
}

func TestSetStore_And_GetStore(t *testing.T) {
	// Initially nil.
	if s := getStore(); s != nil {
		t.Fatal("expected nil store initially")
	}

	db := setupTraceDB(t)
	store := NewStore(db)
	defer store.Close()

	SetStore(store)
	defer SetStore(nil)

	if s := getStore(); s != store {
		t.Fatal("getStore did not return set store")
	}

	// Reset to nil.
	SetStore(nil)
	if s := getStore(); s != nil {
		t.Fatal("expected nil after reset")
	}
}

func TestDriverRegistered(t *testing.T) {
	// The init() in trace.go registers "sqlite-trace".
	drivers := sql.Drivers()
	found := false
	for _, d := range drivers {
		if d == "sqlite-trace" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("sqlite-trace driver not registered")
	}
}

func TestTracingDriver_OpenAndQuery(t *testing.T) {
	// Use the tracing driver for a simple query.
	db, err := sql.Open("sqlite-trace", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Set up a trace store to capture entries.
	traceDB := setupTraceDB(t)
	store := NewStore(traceDB)
	store.Init()
	SetStore(store)
	defer SetStore(nil)

	// Execute a query through the tracing driver.
	db.Exec("CREATE TABLE test (id INTEGER)")
	db.Exec("INSERT INTO test VALUES (1)")

	var val int
	db.QueryRow("SELECT id FROM test").Scan(&val)
	if val != 1 {
		t.Fatalf("query result: got %d", val)
	}

	// Close store to flush.
	store.Close()

	// Verify traces were recorded.
	var count int
	traceDB.QueryRow("SELECT COUNT(*) FROM sql_traces").Scan(&count)
	if count == 0 {
		t.Fatal("no traces recorded through tracing driver")
	}
}

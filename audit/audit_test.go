package audit

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/pkg/kit"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA foreign_keys=ON")
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSQLiteLogger_Init(t *testing.T) {
	db := setupTestDB(t)
	logger := NewSQLiteLogger(db)
	defer logger.Close()

	if err := logger.Init(); err != nil {
		t.Fatal(err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='audit_log'").Scan(&count)
	if count != 1 {
		t.Fatal("audit_log table not created")
	}
}

func TestSQLiteLogger_Log_Sync(t *testing.T) {
	db := setupTestDB(t)
	logger := NewSQLiteLogger(db)
	defer logger.Close()
	logger.Init()

	ctx := context.Background()
	entry := &Entry{
		Action:     "test_action",
		Parameters: `{"key":"value"}`,
	}
	if err := logger.Log(ctx, entry); err != nil {
		t.Fatal(err)
	}

	// Verify defaults were filled.
	if entry.EntryID == "" {
		t.Fatal("entry_id not generated")
	}
	if entry.Timestamp == 0 {
		t.Fatal("timestamp not set")
	}
	if entry.Status != "success" {
		t.Fatalf("status: got %q, want 'success'", entry.Status)
	}
	if entry.Transport != "http" {
		t.Fatalf("transport: got %q, want 'http'", entry.Transport)
	}

	// Verify in DB.
	var action string
	db.QueryRow("SELECT action FROM audit_log WHERE entry_id = ?", entry.EntryID).Scan(&action)
	if action != "test_action" {
		t.Fatalf("DB action: got %q", action)
	}
}

func TestSQLiteLogger_LogAsync(t *testing.T) {
	db := setupTestDB(t)
	logger := NewSQLiteLogger(db)
	logger.Init()

	entry := &Entry{Action: "async_test"}
	logger.LogAsync(entry)

	// Close flushes the buffer.
	logger.Close()

	var count int
	db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='async_test'").Scan(&count)
	if count != 1 {
		t.Fatalf("async entry count: got %d", count)
	}
}

func TestSQLiteLogger_FillDefaults_Error(t *testing.T) {
	db := setupTestDB(t)
	logger := NewSQLiteLogger(db)
	defer logger.Close()
	logger.Init()

	entry := &Entry{
		Action: "failing_op",
		Error:  "something broke",
	}
	logger.Log(context.Background(), entry)

	if entry.Status != "error" {
		t.Fatalf("status for error entry: got %q", entry.Status)
	}
}

func TestSQLiteLogger_WithIDGenerator(t *testing.T) {
	db := setupTestDB(t)
	counter := 0
	gen := func() string {
		counter++
		return "custom_id"
	}

	logger := NewSQLiteLogger(db, WithIDGenerator(gen))
	defer logger.Close()
	logger.Init()

	entry := &Entry{Action: "custom_gen"}
	logger.Log(context.Background(), entry)

	if entry.EntryID != "custom_id" {
		t.Fatalf("custom ID: got %q", entry.EntryID)
	}
}

func TestMiddleware_Success(t *testing.T) {
	db := setupTestDB(t)
	logger := NewSQLiteLogger(db)
	logger.Init()

	base := func(ctx context.Context, req any) (any, error) {
		return "result", nil
	}

	mw := Middleware(logger, "test_op")
	endpoint := mw(base)

	ctx := kit.WithUserID(context.Background(), "usr_1")
	ctx = kit.WithTransport(ctx, "mcp_quic")
	ctx = kit.WithRequestID(ctx, "req_abc")

	resp, err := endpoint(ctx, map[string]string{"foo": "bar"})
	if err != nil {
		t.Fatal(err)
	}
	if resp != "result" {
		t.Fatalf("response: got %v", resp)
	}

	// Close to flush async entries.
	logger.Close()

	var action, userID, transport, status string
	db.QueryRow("SELECT action, user_id, transport, status FROM audit_log WHERE action='test_op'").
		Scan(&action, &userID, &transport, &status)
	if action != "test_op" {
		t.Fatalf("action: got %q", action)
	}
	if userID != "usr_1" {
		t.Fatalf("user_id: got %q", userID)
	}
	if transport != "mcp_quic" {
		t.Fatalf("transport: got %q", transport)
	}
	if status != "success" {
		t.Fatalf("status: got %q", status)
	}
}

func TestMiddleware_Error(t *testing.T) {
	db := setupTestDB(t)
	logger := NewSQLiteLogger(db)
	logger.Init()

	errFail := errors.New("endpoint failed")
	base := func(ctx context.Context, req any) (any, error) {
		return nil, errFail
	}

	mw := Middleware(logger, "fail_op")
	endpoint := mw(base)

	_, err := endpoint(context.Background(), nil)
	if !errors.Is(err, errFail) {
		t.Fatalf("error: got %v", err)
	}

	logger.Close()

	var status, errMsg string
	db.QueryRow("SELECT status, error_message FROM audit_log WHERE action='fail_op'").
		Scan(&status, &errMsg)
	if status != "error" {
		t.Fatalf("status: got %q", status)
	}
	if errMsg != "endpoint failed" {
		t.Fatalf("error_message: got %q", errMsg)
	}
}

func TestSQLiteLogger_BatchFlush(t *testing.T) {
	db := setupTestDB(t)
	logger := NewSQLiteLogger(db)
	logger.Init()

	for i := 0; i < 50; i++ {
		logger.LogAsync(&Entry{Action: "batch_test"})
	}

	// Wait for flush (batch threshold is 32, so at least one flush should happen).
	time.Sleep(100 * time.Millisecond)
	logger.Close()

	var count int
	db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='batch_test'").Scan(&count)
	if count != 50 {
		t.Fatalf("batch count: got %d, want 50", count)
	}
}

package observability

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupObsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA foreign_keys=ON")
	if err := Init(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestInit_CreatesAllTables(t *testing.T) {
	db := setupObsDB(t)
	tables := []string{
		"worker_heartbeats", "metrics_timeseries", "metrics_metadata",
		"audit_log", "business_event_logs", "system_alerts",
		"http_request_logs", "_observability_metadata",
	}
	for _, table := range tables {
		var count int
		db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
		if count != 1 {
			t.Fatalf("table %s not found", table)
		}
	}
}

// --- MetricsManager ---

func TestMetricsManager_RecordAndQuery(t *testing.T) {
	db := setupObsDB(t)
	mm := NewMetricsManager(db, 100, time.Hour)

	mm.Record(&Metric{
		Name:      "cpu_usage",
		Timestamp: time.Now(),
		Value:     42.5,
		Unit:      "percent",
		Labels:    map[string]string{"host": "srv1"},
	})
	mm.RecordSimple("goroutines", 10, "count")

	// Close flushes the buffer (single call, no defer to avoid double-close).
	mm.Close()

	// Re-create for query (Close stops the flush loop).
	mm2 := NewMetricsManager(db, 100, time.Hour)
	defer mm2.Close()

	metrics, err := mm2.Query("cpu_usage", nil, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 {
		t.Fatalf("cpu_usage count: got %d", len(metrics))
	}
	if metrics[0].Value != 42.5 {
		t.Fatalf("value: got %f", metrics[0].Value)
	}
	if metrics[0].Labels["host"] != "srv1" {
		t.Fatalf("labels: got %v", metrics[0].Labels)
	}

	all, err := mm2.Query("", nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all metrics count: got %d", len(all))
	}
}

func TestMetricsManager_QueryWithTimeRange(t *testing.T) {
	db := setupObsDB(t)
	mm := NewMetricsManager(db, 100, time.Hour)

	now := time.Now()
	mm.Record(&Metric{Name: "m1", Timestamp: now.Add(-2 * time.Hour), Value: 1, Unit: "x"})
	mm.Record(&Metric{Name: "m1", Timestamp: now, Value: 2, Unit: "x"})
	mm.Close() // flushes

	// New manager for querying.
	mm2 := NewMetricsManager(db, 100, time.Hour)
	defer mm2.Close()

	start := now.Add(-time.Hour)
	metrics, err := mm2.Query("m1", &start, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 {
		t.Fatalf("time-filtered count: got %d", len(metrics))
	}
}

func TestMetricsManager_Cleanup(t *testing.T) {
	db := setupObsDB(t)
	mm := NewMetricsManager(db, 100, time.Hour)

	old := time.Now().Add(-40 * 24 * time.Hour)
	mm.Record(&Metric{Name: "old_metric", Timestamp: old, Value: 1, Unit: "x"})
	mm.Record(&Metric{Name: "new_metric", Timestamp: time.Now(), Value: 2, Unit: "x"})
	mm.Close() // flushes

	mm2 := NewMetricsManager(db, 100, time.Hour)
	defer mm2.Close()

	deleted, err := mm2.Cleanup(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted: got %d", deleted)
	}
}

// --- HeartbeatWriter ---

func TestCollectRuntimeMetrics(t *testing.T) {
	m := CollectRuntimeMetrics()
	if m.GoroutinesCount <= 0 {
		t.Fatal("goroutines should be > 0")
	}
	if m.MemoryAllocMB <= 0 {
		t.Fatal("memory alloc should be > 0")
	}
}

func TestHeartbeatWriter_WriteHeartbeat(t *testing.T) {
	db := setupObsDB(t)
	hw := NewHeartbeatWriter(db, "test_worker", time.Minute)

	if err := hw.WriteHeartbeat(); err != nil {
		t.Fatal(err)
	}

	var workerName string
	var goroutines int
	db.QueryRow("SELECT worker_name, goroutines_count FROM worker_heartbeats LIMIT 1").
		Scan(&workerName, &goroutines)
	if workerName != "test_worker" {
		t.Fatalf("worker_name: got %q", workerName)
	}
	if goroutines <= 0 {
		t.Fatal("goroutines should be > 0")
	}
}

func TestHeartbeatWriter_StartStop(t *testing.T) {
	db := setupObsDB(t)
	hw := NewHeartbeatWriter(db, "loop_worker", 50*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	hw.Start(ctx)

	// Let a few heartbeats fire.
	time.Sleep(200 * time.Millisecond)
	cancel()
	hw.Stop()

	var count int
	db.QueryRow("SELECT COUNT(*) FROM worker_heartbeats WHERE worker_name='loop_worker'").Scan(&count)
	if count < 2 {
		t.Fatalf("heartbeat count: got %d, want >= 2", count)
	}
}

func TestCleanupHeartbeats(t *testing.T) {
	db := setupObsDB(t)

	// Insert old heartbeat.
	oldTs := time.Now().Add(-40 * 24 * time.Hour).Unix()
	db.Exec(`INSERT INTO worker_heartbeats (worker_name, hostname, worker_pid, timestamp,
		goroutines_count, memory_alloc_mb, memory_sys_mb, gc_count)
		VALUES ('old', 'host', 1, ?, 1, 1.0, 1.0, 1)`, oldTs)

	deleted, err := CleanupHeartbeats(context.Background(), db, 30)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted: got %d", deleted)
	}
}

// --- AuditLogger ---

func TestAuditLogger_LogSync(t *testing.T) {
	db := setupObsDB(t)
	al := NewAuditLogger(db, 100)
	defer al.Close()

	ctx := context.Background()
	entry := &AuditEntry{
		ComponentName: "test",
		OperationType: "create",
		Status:        "success",
		DurationMs:    42,
	}
	if err := al.Log(ctx, entry); err != nil {
		t.Fatal(err)
	}

	if entry.EntryID == "" {
		t.Fatal("entry_id not generated")
	}

	var component string
	db.QueryRow("SELECT component_name FROM audit_log WHERE entry_id=?", entry.EntryID).Scan(&component)
	if component != "test" {
		t.Fatalf("component: got %q", component)
	}
}

func TestAuditLogger_LogAsync(t *testing.T) {
	db := setupObsDB(t)
	al := NewAuditLogger(db, 100)

	al.LogAsync(&AuditEntry{
		ComponentName: "async_test",
		OperationType: "update",
	})
	al.Close()

	var count int
	db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE component_name='async_test'").Scan(&count)
	if count != 1 {
		t.Fatalf("async count: got %d", count)
	}
}

func TestAuditLogger_NewAuditEntry_Success(t *testing.T) {
	db := setupObsDB(t)
	al := NewAuditLogger(db, 100)
	defer al.Close()

	entry := al.NewAuditEntry("comp", "op", map[string]string{"k": "v"}, "result", nil, 100*time.Millisecond)
	if entry.Status != "success" {
		t.Fatalf("status: got %q", entry.Status)
	}
	if entry.Parameters == "" {
		t.Fatal("parameters not marshalled")
	}
	if entry.Result == "" {
		t.Fatal("result not marshalled")
	}
	if entry.DurationMs != 100 {
		t.Fatalf("duration_ms: got %d", entry.DurationMs)
	}
}

func TestAuditLogger_NewAuditEntry_Error(t *testing.T) {
	db := setupObsDB(t)
	al := NewAuditLogger(db, 100)
	defer al.Close()

	entry := al.NewAuditEntry("comp", "op", nil, nil, errors.New("boom"), 50*time.Millisecond)
	if entry.Status != "error" {
		t.Fatalf("status: got %q", entry.Status)
	}
	if entry.ErrorMessage != "boom" {
		t.Fatalf("error_message: got %q", entry.ErrorMessage)
	}
}

func TestAuditLogger_Query(t *testing.T) {
	db := setupObsDB(t)
	al := NewAuditLogger(db, 100)

	al.Log(context.Background(), &AuditEntry{ComponentName: "svc_a", OperationType: "create", Status: "success"})
	al.Log(context.Background(), &AuditEntry{ComponentName: "svc_b", OperationType: "delete", Status: "error"})

	comp := "svc_a"
	entries, err := al.Query(context.Background(), &AuditFilter{ComponentName: &comp, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("filtered count: got %d", len(entries))
	}
	if entries[0].ComponentName != "svc_a" {
		t.Fatalf("component: got %q", entries[0].ComponentName)
	}

	al.Close()
}

func TestAuditLogger_Cleanup(t *testing.T) {
	db := setupObsDB(t)
	al := NewAuditLogger(db, 100)

	oldTs := time.Now().Add(-40 * 24 * time.Hour)
	al.Log(context.Background(), &AuditEntry{
		ComponentName: "old",
		OperationType: "test",
		Timestamp:     oldTs,
	})
	al.Log(context.Background(), &AuditEntry{
		ComponentName: "new",
		OperationType: "test",
	})

	deleted, err := al.Cleanup(context.Background(), 30)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted: got %d", deleted)
	}

	al.Close()
}

func TestAuditLogger_WithIDGenerator(t *testing.T) {
	db := setupObsDB(t)
	gen := func() string { return "fixed_id" }
	al := NewAuditLogger(db, 100, WithAuditIDGenerator(gen))
	defer al.Close()

	entry := &AuditEntry{ComponentName: "test", OperationType: "op"}
	al.Log(context.Background(), entry)
	if entry.EntryID != "fixed_id" {
		t.Fatalf("custom ID: got %q", entry.EntryID)
	}
}

// --- EventLogger ---

func TestEventLogger_LogEvent(t *testing.T) {
	db := setupObsDB(t)
	el := NewEventLogger(db)

	el.LogEvent(context.Background(), BusinessEvent{
		EventType:   "user_created",
		ServiceName: "auth",
		EntityType:  "user",
		EntityID:    "usr_1",
		Action:      "create",
		Success:     true,
	})

	var eventType, action string
	db.QueryRow("SELECT event_type, action FROM business_event_logs LIMIT 1").Scan(&eventType, &action)
	if eventType != "user_created" {
		t.Fatalf("event_type: got %q", eventType)
	}
	if action != "create" {
		t.Fatalf("action: got %q", action)
	}
}

func TestEventLogger_WithIDGenerator(t *testing.T) {
	db := setupObsDB(t)
	gen := func() string { return "evt_custom" }
	el := NewEventLogger(db, WithEventIDGenerator(gen))

	el.LogEvent(context.Background(), BusinessEvent{
		EventType:   "test",
		ServiceName: "test",
		Action:      "test",
		Success:     true,
	})

	var eventID string
	db.QueryRow("SELECT event_id FROM business_event_logs LIMIT 1").Scan(&eventID)
	if eventID != "evt_custom" {
		t.Fatalf("custom event_id: got %q", eventID)
	}
}

// --- Retention Cleanup ---

func TestCleanup_Retention(t *testing.T) {
	db := setupObsDB(t)

	oldTs := time.Now().Add(-40 * 24 * time.Hour).Unix()
	db.Exec("INSERT INTO http_request_logs (method, path, created_at) VALUES ('GET', '/test', ?)", oldTs)
	db.Exec("INSERT INTO business_event_logs (event_id, event_type, service_name, action, success, created_at) VALUES ('e1', 'test', 'svc', 'act', 1, ?)", oldTs)

	err := Cleanup(context.Background(), db, RetentionConfig{
		HTTPLogsDays:  30,
		EventLogsDays: 30,
	})
	if err != nil {
		t.Fatal(err)
	}

	var httpCount, eventCount int
	db.QueryRow("SELECT COUNT(*) FROM http_request_logs").Scan(&httpCount)
	db.QueryRow("SELECT COUNT(*) FROM business_event_logs").Scan(&eventCount)
	if httpCount != 0 {
		t.Fatalf("http_request_logs: got %d", httpCount)
	}
	if eventCount != 0 {
		t.Fatalf("business_event_logs: got %d", eventCount)
	}
}

func TestCleanup_SkipsZeroDays(t *testing.T) {
	db := setupObsDB(t)

	oldTs := time.Now().Add(-40 * 24 * time.Hour).Unix()
	db.Exec("INSERT INTO http_request_logs (method, path, created_at) VALUES ('GET', '/test', ?)", oldTs)

	err := Cleanup(context.Background(), db, RetentionConfig{
		HTTPLogsDays: 0, // disabled
	})
	if err != nil {
		t.Fatal(err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM http_request_logs").Scan(&count)
	if count != 1 {
		t.Fatalf("should not clean when days=0: got %d", count)
	}
}

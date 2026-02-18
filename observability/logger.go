package observability

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/hazyhaar/pkg/idgen"
)

// BusinessEvent represents a domain-level event to record.
type BusinessEvent struct {
	EventType   string
	ServiceName string
	EntityType  string
	EntityID    string
	UserID      string
	Action      string
	Details     string // optional JSON
	Success     bool
}

// EventLogger writes business events and manages retention cleanup.
type EventLogger struct {
	db    *sql.DB
	newID idgen.Generator
}

// EventLoggerOption configures an EventLogger.
type EventLoggerOption func(*EventLogger)

// WithEventIDGenerator sets a custom ID generator for event IDs.
func WithEventIDGenerator(gen idgen.Generator) EventLoggerOption {
	return func(l *EventLogger) { l.newID = gen }
}

// NewEventLogger creates a logger backed by the given observability database.
func NewEventLogger(db *sql.DB, opts ...EventLoggerOption) *EventLogger {
	l := &EventLogger{
		db:    db,
		newID: idgen.Prefixed("evt_", idgen.Default),
	}
	for _, o := range opts {
		o(l)
	}
	return l
}

// LogEvent records a business event. Non-blocking: errors are logged via slog
// but do not propagate, so a failing observability store never blocks the app.
func (l *EventLogger) LogEvent(ctx context.Context, event BusinessEvent) {
	eventID := l.newID()
	_, err := l.db.ExecContext(ctx, `
		INSERT INTO business_event_logs (
			event_id, event_type, service_name, entity_type, entity_id,
			user_id, action, details, success, created_at
		) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		eventID, event.EventType, event.ServiceName, event.EntityType, event.EntityID,
		event.UserID, event.Action, event.Details, event.Success, time.Now().Unix())
	if err != nil {
		slog.Error("observability event log failed", "error", err, "event_type", event.EventType)
	}
}

// LogHeartbeat records a lightweight heartbeat row (for services that prefer
// the simpler Logger interface instead of HeartbeatWriter).
func (l *EventLogger) LogHeartbeat(ctx context.Context, workerName string, workerPID int, machineName string) {
	heartbeatID := l.newID()
	_, err := l.db.ExecContext(ctx, `
		INSERT INTO worker_heartbeats (
			heartbeat_id, worker_name, hostname, worker_pid, timestamp
		) VALUES (?,?,?,?,?)`,
		heartbeatID, workerName, machineName, workerPID, time.Now().Unix())
	if err != nil {
		slog.Warn("heartbeat log failed", "error", err, "worker", workerName)
	}
}

// RetentionConfig specifies per-table retention in days. Zero means no cleanup.
type RetentionConfig struct {
	HTTPLogsDays      int
	EventLogsDays     int
	HeartbeatsDays    int
	RunVacuumAfter    bool
}

// Cleanup deletes records exceeding the retention thresholds.
func Cleanup(ctx context.Context, db *sql.DB, cfg RetentionConfig) error {
	now := time.Now().Unix()

	// allowedTables and allowedColumns are whitelists to prevent SQL injection
	// if this pattern is ever refactored to accept external input.
	allowedTables := map[string]bool{
		"http_request_logs":    true,
		"business_event_logs":  true,
		"worker_heartbeats":    true,
	}
	allowedColumns := map[string]bool{
		"created_at": true,
		"timestamp":  true,
	}

	type cleanupTarget struct {
		table  string
		column string
		days   int
	}
	targets := []cleanupTarget{
		{"http_request_logs", "created_at", cfg.HTTPLogsDays},
		{"business_event_logs", "created_at", cfg.EventLogsDays},
		{"worker_heartbeats", "timestamp", cfg.HeartbeatsDays},
	}

	for _, t := range targets {
		if t.days <= 0 {
			continue
		}
		if !allowedTables[t.table] || !allowedColumns[t.column] {
			return fmt.Errorf("cleanup: invalid table/column %s/%s", t.table, t.column)
		}
		cutoff := now - int64(t.days*86400)
		q := fmt.Sprintf("DELETE FROM %s WHERE %s < ?", t.table, t.column)
		if _, err := db.ExecContext(ctx, q, cutoff); err != nil {
			return fmt.Errorf("cleanup %s: %w", t.table, err)
		}
	}

	if cfg.RunVacuumAfter {
		if _, err := db.ExecContext(ctx, "VACUUM"); err != nil {
			return fmt.Errorf("vacuum: %w", err)
		}
	}
	return nil
}

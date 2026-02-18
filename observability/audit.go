package observability

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hazyhaar/pkg/idgen"
)

// AuditEntry is a single operation record in the audit trail.
type AuditEntry struct {
	EntryID       string
	Timestamp     time.Time
	ComponentName string // e.g. "orchestrator", "chunker", "embedder"
	OperationType string // e.g. "workflow_start", "task_dispatch"

	UserID    string
	SessionID string
	RequestID string

	Parameters   string // JSON
	Result       string // JSON
	ErrorCode    string
	ErrorMessage string
	DurationMs   int64

	Status   string // "success", "error", "timeout", "cancelled"
	Metadata string // free-form JSON
}

// AuditFilter controls query results from the audit log.
type AuditFilter struct {
	StartTime     *time.Time
	EndTime       *time.Time
	ComponentName *string
	OperationType *string
	Status        *string
	Limit         int    // default 100
	Offset        int
	OrderBy       string // "timestamp" or "duration_ms"
	OrderDir      string // "ASC" or "DESC"
}

// AuditLogger persists operation-level audit entries asynchronously.
type AuditLogger struct {
	db    *sql.DB
	newID idgen.Generator
	ch    chan *AuditEntry
	stop  chan struct{}
	done  chan struct{}
}

// AuditOption configures an AuditLogger.
type AuditOption func(*AuditLogger)

// WithAuditIDGenerator sets a custom ID generator for audit entry IDs.
func WithAuditIDGenerator(gen idgen.Generator) AuditOption {
	return func(a *AuditLogger) { a.newID = gen }
}

// NewAuditLogger creates an async audit logger. Recommended bufferSize: 1000.
func NewAuditLogger(db *sql.DB, bufferSize int, opts ...AuditOption) *AuditLogger {
	a := &AuditLogger{
		db:    db,
		newID: idgen.Prefixed("audit_", idgen.Default),
		ch:    make(chan *AuditEntry, bufferSize),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	for _, o := range opts {
		o(a)
	}
	go a.flushLoop()
	return a
}

// Log inserts an audit entry synchronously.
func (a *AuditLogger) Log(ctx context.Context, entry *AuditEntry) error {
	a.fillDefaults(entry)
	return a.insert(ctx, entry)
}

// LogAsync queues an entry for async persistence.
// Falls back to synchronous insert if the buffer is full.
func (a *AuditLogger) LogAsync(entry *AuditEntry) {
	a.fillDefaults(entry)
	select {
	case a.ch <- entry:
	default:
		slog.Warn("observability audit buffer full, sync fallback", "component", entry.ComponentName)
		if err := a.insert(context.Background(), entry); err != nil {
			slog.Error("observability audit: sync fallback failed", "error", err)
		}
	}
}

// NewAuditEntry is a convenience factory that builds an AuditEntry from
// operation parameters, result and error. Params and result are marshalled to JSON.
func (a *AuditLogger) NewAuditEntry(component, operation string, params interface{}, result interface{}, err error, duration time.Duration) *AuditEntry {
	entry := &AuditEntry{
		EntryID:       a.newID(),
		Timestamp:     time.Now(),
		ComponentName: component,
		OperationType: operation,
		DurationMs:    duration.Milliseconds(),
	}

	if params != nil {
		if b, e := json.Marshal(params); e == nil {
			entry.Parameters = string(b)
		}
	}
	if err != nil {
		entry.Status = "error"
		entry.ErrorMessage = err.Error()
	} else {
		entry.Status = "success"
		if result != nil {
			if b, e := json.Marshal(result); e == nil {
				entry.Result = string(b)
			}
		}
	}
	return entry
}

// Query retrieves audit entries matching the given filter.
func (a *AuditLogger) Query(ctx context.Context, f *AuditFilter) ([]*AuditEntry, error) {
	q := `SELECT entry_id, timestamp, component_name, operation_type,
		user_id, session_id, request_id, parameters, result,
		error_code, error_message, duration_ms, status, metadata
		FROM audit_log WHERE 1=1`
	var args []interface{}

	if f.StartTime != nil {
		q += " AND timestamp >= ?"
		args = append(args, f.StartTime.Unix())
	}
	if f.EndTime != nil {
		q += " AND timestamp <= ?"
		args = append(args, f.EndTime.Unix())
	}
	if f.ComponentName != nil {
		q += " AND component_name = ?"
		args = append(args, *f.ComponentName)
	}
	if f.OperationType != nil {
		q += " AND operation_type = ?"
		args = append(args, *f.OperationType)
	}
	if f.Status != nil {
		q += " AND status = ?"
		args = append(args, *f.Status)
	}

	orderBy := "timestamp"
	if f.OrderBy != "" {
		switch f.OrderBy {
		case "timestamp", "duration_ms", "component_name", "status":
			orderBy = f.OrderBy
		default:
			return nil, fmt.Errorf("invalid order_by column: %q", f.OrderBy)
		}
	}
	orderDir := "DESC"
	if f.OrderDir != "" {
		switch strings.ToUpper(f.OrderDir) {
		case "ASC", "DESC":
			orderDir = strings.ToUpper(f.OrderDir)
		default:
			return nil, fmt.Errorf("invalid order_dir: %q", f.OrderDir)
		}
	}
	q += fmt.Sprintf(" ORDER BY %s %s", orderBy, orderDir)

	limit := 100
	if f.Limit > 0 {
		limit = f.Limit
	}
	q += " LIMIT ?"
	args = append(args, limit)
	if f.Offset > 0 {
		q += " OFFSET ?"
		args = append(args, f.Offset)
	}

	rows, err := a.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query audit log: %w", err)
	}
	defer rows.Close()

	var entries []*AuditEntry
	for rows.Next() {
		var e AuditEntry
		var ts int64
		var userID, sessionID, requestID sql.NullString
		var result, errorCode, errorMessage, metadata sql.NullString
		var durationMs sql.NullInt64

		if err := rows.Scan(
			&e.EntryID, &ts, &e.ComponentName, &e.OperationType,
			&userID, &sessionID, &requestID,
			&e.Parameters, &result, &errorCode, &errorMessage,
			&durationMs, &e.Status, &metadata,
		); err != nil {
			return nil, fmt.Errorf("scan audit entry: %w", err)
		}

		e.Timestamp = time.Unix(ts, 0)
		if userID.Valid {
			e.UserID = userID.String
		}
		if sessionID.Valid {
			e.SessionID = sessionID.String
		}
		if requestID.Valid {
			e.RequestID = requestID.String
		}
		if result.Valid {
			e.Result = result.String
		}
		if errorCode.Valid {
			e.ErrorCode = errorCode.String
		}
		if errorMessage.Valid {
			e.ErrorMessage = errorMessage.String
		}
		if durationMs.Valid {
			e.DurationMs = durationMs.Int64
		}
		if metadata.Valid {
			e.Metadata = metadata.String
		}
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}

// Cleanup deletes audit entries older than retentionDays.
func (a *AuditLogger) Cleanup(ctx context.Context, retentionDays int) (int64, error) {
	threshold := time.Now().AddDate(0, 0, -retentionDays).Unix()
	result, err := a.db.ExecContext(ctx, "DELETE FROM audit_log WHERE timestamp < ?", threshold)
	if err != nil {
		return 0, fmt.Errorf("cleanup audit log: %w", err)
	}
	return result.RowsAffected()
}

// Close drains the buffer and stops the flush goroutine.
func (a *AuditLogger) Close() error {
	close(a.stop)
	<-a.done
	return nil
}

func (a *AuditLogger) fillDefaults(e *AuditEntry) {
	if e.EntryID == "" {
		e.EntryID = a.newID()
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	if e.Status == "" {
		if e.ErrorMessage != "" {
			e.Status = "error"
		} else {
			e.Status = "success"
		}
	}
}

func (a *AuditLogger) flushLoop() {
	defer close(a.done)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	batch := make([]*AuditEntry, 0, 100)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		tx, err := a.db.BeginTx(ctx, nil)
		if err != nil {
			slog.Error("observability audit: begin tx", "error", err)
			return
		}
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO audit_log
			(entry_id, timestamp, component_name, operation_type,
			 user_id, session_id, request_id,
			 parameters, result, error_code, error_message, duration_ms,
			 status, metadata)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			tx.Rollback()
			slog.Error("observability audit: prepare", "error", err)
			return
		}
		defer stmt.Close()

		for _, e := range batch {
			if _, err := stmt.ExecContext(ctx,
				e.EntryID, e.Timestamp.Unix(), e.ComponentName, e.OperationType,
				e.UserID, e.SessionID, e.RequestID,
				e.Parameters, e.Result, e.ErrorCode, e.ErrorMessage, e.DurationMs,
				e.Status, e.Metadata,
			); err != nil {
				slog.Error("observability audit: insert", "error", err, "entry_id", e.EntryID)
			}
		}
		if err := tx.Commit(); err != nil {
			slog.Error("observability audit: commit", "error", err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-a.stop:
			// drain channel
			for {
				select {
				case e := <-a.ch:
					batch = append(batch, e)
				default:
					flush()
					return
				}
			}
		case e := <-a.ch:
			batch = append(batch, e)
			if len(batch) >= 100 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (a *AuditLogger) insert(ctx context.Context, e *AuditEntry) error {
	_, err := a.db.ExecContext(ctx, `INSERT INTO audit_log
		(entry_id, timestamp, component_name, operation_type,
		 user_id, session_id, request_id,
		 parameters, result, error_code, error_message, duration_ms,
		 status, metadata)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.EntryID, e.Timestamp.Unix(), e.ComponentName, e.OperationType,
		e.UserID, e.SessionID, e.RequestID,
		e.Parameters, e.Result, e.ErrorCode, e.ErrorMessage, e.DurationMs,
		e.Status, e.Metadata)
	return err
}

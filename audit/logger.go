package audit

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/hazyhaar/pkg/idgen"
)

const Schema = `
CREATE TABLE IF NOT EXISTS audit_log (
	entry_id TEXT PRIMARY KEY,
	timestamp INTEGER NOT NULL,
	action TEXT NOT NULL,
	transport TEXT NOT NULL DEFAULT 'http',
	user_id TEXT,
	request_id TEXT,
	parameters TEXT,
	result TEXT,
	error_message TEXT,
	duration_ms INTEGER,
	status TEXT NOT NULL DEFAULT 'success'
);
CREATE INDEX IF NOT EXISTS idx_audit_log_time ON audit_log(timestamp);
CREATE INDEX IF NOT EXISTS idx_audit_log_action ON audit_log(action);
CREATE INDEX IF NOT EXISTS idx_audit_log_user ON audit_log(user_id);
`

// SQLiteLogger writes audit entries to the audit_log table asynchronously.
type SQLiteLogger struct {
	db    *sql.DB
	newID idgen.Generator
	ch    chan *Entry
	done  chan struct{}
	once  sync.Once
}

// Option configures a SQLiteLogger.
type Option func(*SQLiteLogger)

// WithIDGenerator sets a custom ID generator for audit entry IDs.
func WithIDGenerator(gen idgen.Generator) Option {
	return func(l *SQLiteLogger) { l.newID = gen }
}

func NewSQLiteLogger(sqlDB *sql.DB, opts ...Option) *SQLiteLogger {
	l := &SQLiteLogger{
		db:    sqlDB,
		newID: idgen.Prefixed("aud_", idgen.Default),
		ch:    make(chan *Entry, 256),
		done:  make(chan struct{}),
	}
	for _, o := range opts {
		o(l)
	}
	go l.flushLoop()
	return l
}

func (l *SQLiteLogger) Init() error {
	_, err := l.db.Exec(Schema)
	return err
}

func (l *SQLiteLogger) Log(_ context.Context, entry *Entry) error {
	l.fillDefaults(entry)
	return l.insert(entry)
}

func (l *SQLiteLogger) LogAsync(entry *Entry) {
	l.fillDefaults(entry)
	select {
	case l.ch <- entry:
	default:
		slog.Warn("audit buffer full, dropping entry", "action", entry.Action)
	}
}

func (l *SQLiteLogger) Close() error {
	l.once.Do(func() {
		close(l.ch)
		<-l.done
	})
	return nil
}

func (l *SQLiteLogger) fillDefaults(e *Entry) {
	if e.EntryID == "" {
		e.EntryID = l.newID()
	}
	if e.Timestamp == 0 {
		e.Timestamp = time.Now().Unix()
	}
	if e.Status == "" {
		if e.Error != "" {
			e.Status = "error"
		} else {
			e.Status = "success"
		}
	}
	if e.Transport == "" {
		e.Transport = "http"
	}
}

func (l *SQLiteLogger) flushLoop() {
	defer close(l.done)
	batch := make([]*Entry, 0, 32)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case entry, ok := <-l.ch:
			if !ok {
				l.flushBatch(batch)
				return
			}
			batch = append(batch, entry)
			if len(batch) >= 32 {
				l.flushBatch(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				l.flushBatch(batch)
				batch = batch[:0]
			}
		}
	}
}

func (l *SQLiteLogger) flushBatch(batch []*Entry) {
	for _, e := range batch {
		if err := l.insert(e); err != nil {
			slog.Error("audit write failed", "error", err, "action", e.Action)
		}
	}
}

func (l *SQLiteLogger) insert(e *Entry) error {
	_, err := l.db.Exec(`
		INSERT INTO audit_log (entry_id, timestamp, action, transport, user_id, request_id,
			parameters, result, error_message, duration_ms, status)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		e.EntryID, e.Timestamp, e.Action, e.Transport, e.UserID, e.RequestID,
		e.Parameters, e.Result, e.Error, e.DurationMs, e.Status)
	return err
}

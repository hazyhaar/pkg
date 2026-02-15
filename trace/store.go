package trace

import (
	"database/sql"
	"log/slog"
	"sync"
	"time"
)

// Schema for the sql_traces table. Call Store.Init() or apply manually.
const Schema = `
CREATE TABLE IF NOT EXISTS sql_traces (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	trace_id TEXT,
	op TEXT NOT NULL,
	query TEXT NOT NULL,
	duration_us INTEGER NOT NULL,
	error TEXT,
	timestamp INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sql_traces_ts ON sql_traces(timestamp);
CREATE INDEX IF NOT EXISTS idx_sql_traces_tid ON sql_traces(trace_id) WHERE trace_id != '';
CREATE INDEX IF NOT EXISTS idx_sql_traces_slow ON sql_traces(duration_us) WHERE duration_us > 100000;
`

// Store persists SQL trace entries to a SQLite table asynchronously.
// It MUST be opened with the raw "sqlite" driver (not "sqlite-trace") to avoid
// infinite recursion.
type Store struct {
	db   *sql.DB
	ch   chan *Entry
	done chan struct{}
	once sync.Once
}

// NewStore creates a trace store backed by the given database connection.
// The db should use the raw "sqlite" driver to avoid tracing its own writes.
func NewStore(db *sql.DB) *Store {
	s := &Store{
		db:   db,
		ch:   make(chan *Entry, 1024),
		done: make(chan struct{}),
	}
	go s.flushLoop()
	return s
}

// Init creates the sql_traces table if it doesn't exist.
func (s *Store) Init() error {
	_, err := s.db.Exec(Schema)
	return err
}

// RecordAsync queues an entry for async persistence. Non-blocking; drops if buffer full.
func (s *Store) RecordAsync(e *Entry) {
	select {
	case s.ch <- e:
	default:
		// buffer full — drop silently to avoid backpressure on the app
	}
}

// Close drains the buffer and stops the flush goroutine.
func (s *Store) Close() error {
	s.once.Do(func() {
		close(s.ch)
		<-s.done
	})
	return nil
}

func (s *Store) flushLoop() {
	defer close(s.done)

	batch := make([]*Entry, 0, 64)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case e, ok := <-s.ch:
			if !ok {
				s.flushBatch(batch)
				return
			}
			batch = append(batch, e)
			if len(batch) >= 64 {
				s.flushBatch(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				s.flushBatch(batch)
				batch = batch[:0]
			}
		}
	}
}

func (s *Store) flushBatch(batch []*Entry) {
	if len(batch) == 0 {
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		slog.Error("trace store: begin tx", "error", err)
		return
	}

	stmt, err := tx.Prepare(`INSERT INTO sql_traces (trace_id, op, query, duration_us, error, timestamp)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		slog.Error("trace store: prepare", "error", err)
		return
	}
	defer stmt.Close()

	for _, e := range batch {
		if _, err := stmt.Exec(e.TraceID, e.Op, e.Query, e.DurationUs, e.Error, e.Timestamp); err != nil {
			slog.Error("trace store: insert", "error", err)
		}
	}

	if err := tx.Commit(); err != nil {
		slog.Error("trace store: commit", "error", err)
	}
}

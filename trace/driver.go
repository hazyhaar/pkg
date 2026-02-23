package trace

import (
	"context"
	"database/sql/driver"
	"log/slog"
	"strings"
	"time"

	"github.com/hazyhaar/pkg/kit"
)

// TracingDriver wraps the modernc.org/sqlite driver, intercepting every
// Exec and Query at the database/sql/driver level.
//
// Registered as "sqlite-trace" in init(). Open connections with
// sql.Open("sqlite-trace", path) to get automatic tracing.
type TracingDriver struct {
	driver.Driver
}

func (d *TracingDriver) Open(name string) (driver.Conn, error) {
	conn, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return &tracingConn{Conn: conn}, nil
}

type tracingConn struct {
	driver.Conn
}

func (c *tracingConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &tracingStmt{Stmt: stmt, query: query}, nil
}

func (c *tracingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if pc, ok := c.Conn.(driver.ConnPrepareContext); ok {
		stmt, err := pc.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		return &tracingStmt{Stmt: stmt, query: query}, nil
	}
	return c.Prepare(query)
}

func (c *tracingConn) Begin() (driver.Tx, error) {
	return c.Conn.Begin()
}

type tracingStmt struct {
	driver.Stmt
	query string
}

func (s *tracingStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	start := time.Now()
	var result driver.Result
	var err error
	if ec, ok := s.Stmt.(driver.StmtExecContext); ok {
		result, err = ec.ExecContext(ctx, args)
	} else {
		result, err = s.Stmt.Exec(namedToValues(args))
	}
	s.record(ctx, "Exec", time.Since(start), err)
	return result, err
}

func (s *tracingStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	start := time.Now()
	var rows driver.Rows
	var err error
	if qc, ok := s.Stmt.(driver.StmtQueryContext); ok {
		rows, err = qc.QueryContext(ctx, args)
	} else {
		rows, err = s.Stmt.Query(namedToValues(args))
	}
	s.record(ctx, "Query", time.Since(start), err)
	return rows, err
}

func (s *tracingStmt) Exec(args []driver.Value) (driver.Result, error) {
	start := time.Now()
	result, err := s.Stmt.Exec(args)
	s.record(context.Background(), "Exec", time.Since(start), err)
	return result, err
}

func (s *tracingStmt) Query(args []driver.Value) (driver.Rows, error) {
	start := time.Now()
	rows, err := s.Stmt.Query(args)
	s.record(context.Background(), "Query", time.Since(start), err)
	return rows, err
}

func (s *tracingStmt) record(ctx context.Context, op string, d time.Duration, err error) {
	// Skip PRAGMA noise (dbsync watcher polls PRAGMA data_version every 200ms).
	// Still record if slow (>10ms) or errored — those are worth investigating.
	if err == nil && d < 10*time.Millisecond && strings.HasPrefix(s.query, "PRAGMA ") {
		return
	}

	traceID := kit.GetTraceID(ctx)

	// 1. Structured logging via slog
	level := slog.LevelDebug
	if err != nil {
		level = slog.LevelError
	} else if d > 100*time.Millisecond {
		level = slog.LevelWarn
	}

	attrs := []slog.Attr{
		slog.String("component", "sql"),
		slog.String("op", op),
		slog.String("query", s.query),
		slog.Duration("duration", d),
	}
	if traceID != "" {
		attrs = append(attrs, slog.String("trace_id", traceID))
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	slog.LogAttrs(ctx, level, "SQL", attrs...)

	// 2. Persistence (async, non-blocking) — local Store or RemoteStore.
	if store := getStore(); store != nil {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		store.RecordAsync(&Entry{
			TraceID:    traceID,
			Op:         op,
			Query:      s.query,
			DurationUs: d.Microseconds(),
			Error:      errMsg,
			Timestamp:  time.Now().UnixMicro(),
		})
	}
}

func namedToValues(named []driver.NamedValue) []driver.Value {
	vals := make([]driver.Value, len(named))
	for i, nv := range named {
		vals[i] = nv.Value
	}
	return vals
}

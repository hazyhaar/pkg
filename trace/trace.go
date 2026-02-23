// Package trace provides transparent SQL tracing for modernc.org/sqlite.
//
// It registers a "sqlite-trace" driver that wraps the standard "sqlite" driver,
// intercepting every Exec and Query at the database/sql/driver level. No
// application code changes are needed beyond switching the driver name:
//
//	import _ "github.com/hazyhaar/pkg/trace"  // registers "sqlite-trace"
//
//	// Trace store (opened with raw "sqlite" to avoid recursion)
//	traceDB, _ := sql.Open("sqlite", "traces.db")
//	store := trace.NewStore(traceDB)
//	store.Init()
//	trace.SetStore(store)
//
//	// Application DB — all queries are now traced automatically
//	db, _ := sql.Open("sqlite-trace", "app.db")
//
// Without a Store (SetStore not called or nil), the driver still logs every
// query via slog with adaptive levels (Debug, Warn >100ms, Error on failure).
// Trace IDs are read from context via kit.GetTraceID for request correlation.
package trace

import (
	"database/sql"
	"sync"

	sqlite "modernc.org/sqlite"
)

// Entry is a single SQL trace record.
type Entry struct {
	TraceID    string // correlation with HTTP/MCP request
	Op         string // "Exec" or "Query"
	Query      string // SQL statement
	DurationUs int64  // microseconds
	Error      string // empty if success
	Timestamp  int64  // unix microseconds
}

// Recorder is the interface for trace persistence backends.
// Store (local SQLite) and RemoteStore (HTTP POST to BO) both implement it.
type Recorder interface {
	RecordAsync(e *Entry)
	Close() error
}

// global store for persistence (nil = slog-only, no SQLite persistence)
var (
	globalStore Recorder
	storeMu     sync.RWMutex
)

// SetStore sets the global trace recorder for persistence.
// Accepts a Store (local SQLite) or RemoteStore (HTTP to BO).
// Pass nil to disable persistence (slog-only mode).
func SetStore(s Recorder) {
	storeMu.Lock()
	globalStore = s
	storeMu.Unlock()
}

func getStore() Recorder {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return globalStore
}

func init() {
	sql.Register("sqlite-trace", &TracingDriver{
		Driver: &sqlite.Driver{},
	})
}

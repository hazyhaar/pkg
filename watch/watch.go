// Package watch provides a generic "poll SQLite, detect change, debounce,
// reload" loop. It standardises the reactive pattern used across the chassis
// so that every consumer gets consistent intervals, debounce windows, and
// observability for free.
//
// Typical usage:
//
//	w := watch.New(db, watch.Options{Interval: 200*time.Millisecond, Debounce: 500*time.Millisecond})
//	go w.OnChange(ctx, func() error { return service.Reload() })
package watch

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ChangeDetector reads a version token from the database. Two calls that
// return different values mean "something changed". The concrete type is
// deliberately int64 — it maps naturally to PRAGMA data_version,
// PRAGMA user_version, or a MAX(updated_at) query.
type ChangeDetector func(ctx context.Context, db *sql.DB) (int64, error)

// Options tunes the watcher behaviour.
type Options struct {
	// Interval is the polling frequency. Default: 1s.
	Interval time.Duration
	// Debounce is the quiet period after a change is detected before the
	// action fires. If more changes arrive during the window the timer
	// resets. 0 means fire immediately. Default: 0.
	Debounce time.Duration
	// Detector overrides the default PragmaDataVersion detector.
	Detector ChangeDetector
	// Logger overrides the default slog logger.
	Logger *slog.Logger
}

func (o *Options) defaults() {
	if o.Interval <= 0 {
		o.Interval = time.Second
	}
	if o.Detector == nil {
		o.Detector = PragmaDataVersion
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
}

// Watcher polls a SQLite database for changes and runs an action when a
// change is detected. It is safe for concurrent use.
type Watcher struct {
	db   *sql.DB
	opts Options

	// version is the last observed version token.
	version atomic.Int64

	// versionMu + versionCond broadcast when the version advances,
	// enabling WaitForVersion.
	versionMu   sync.Mutex
	versionCond *sync.Cond

	// Counters for observability (exported via Stats).
	checks   atomic.Int64
	changes  atomic.Int64
	errors   atomic.Int64
	reloads  atomic.Int64
	reloadNs atomic.Int64
}

// Stats are point-in-time counters.
type Stats struct {
	Checks          int64         `json:"checks"`
	ChangesDetected int64         `json:"changes_detected"`
	Errors          int64         `json:"errors"`
	Reloads         int64         `json:"reloads"`
	AvgReloadTime   time.Duration `json:"avg_reload_time"`
}

// New creates a Watcher. Call OnChange to start the loop.
func New(db *sql.DB, opts Options) *Watcher {
	opts.defaults()
	w := &Watcher{db: db, opts: opts}
	w.versionCond = sync.NewCond(&w.versionMu)
	return w
}

// Stats returns the current counters.
func (w *Watcher) Stats() Stats {
	s := Stats{
		Checks:          w.checks.Load(),
		ChangesDetected: w.changes.Load(),
		Errors:          w.errors.Load(),
		Reloads:         w.reloads.Load(),
	}
	if s.Reloads > 0 {
		s.AvgReloadTime = time.Duration(w.reloadNs.Load() / s.Reloads)
	}
	return s
}

// Version returns the last observed version token.
func (w *Watcher) Version() int64 { return w.version.Load() }

// OnChange blocks until ctx is cancelled, polling at opts.Interval.
// When the detector reports a version change and the debounce window
// passes without further changes, action is called.
//
// If action returns an error the version is NOT advanced — the action
// will be retried on the next poll cycle.
func (w *Watcher) OnChange(ctx context.Context, action func() error) {
	log := w.opts.Logger

	// Seed initial version.
	v, err := w.opts.Detector(ctx, w.db)
	if err != nil {
		log.Warn("watch: initial version check failed", "error", err)
	} else {
		w.setVersion(v)
	}

	ticker := time.NewTicker(w.opts.Interval)
	defer ticker.Stop()

	var debounceTimer *time.Timer
	var debounceCh <-chan time.Time
	pendingVersion := int64(-1)

	log.Info("watch: started", "interval", w.opts.Interval, "debounce", w.opts.Debounce)

	for {
		select {
		case <-ctx.Done():
			log.Info("watch: stopped")
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return

		case <-ticker.C:
			w.checks.Add(1)
			cur, err := w.opts.Detector(ctx, w.db)
			if err != nil {
				w.errors.Add(1)
				log.Warn("watch: version check failed", "error", err)
				continue
			}
			if cur != w.version.Load() && cur != pendingVersion {
				w.changes.Add(1)
				pendingVersion = cur

				if w.opts.Debounce <= 0 {
					// No debounce — fire immediately.
					w.fire(ctx, log, action, pendingVersion)
					pendingVersion = -1
				} else {
					// (Re)start debounce timer — only when the pending
					// version actually changed, not on every poll cycle.
					if debounceTimer != nil {
						debounceTimer.Stop()
					}
					debounceTimer = time.NewTimer(w.opts.Debounce)
					debounceCh = debounceTimer.C
					log.Debug("watch: change detected, debouncing", "pending_version", cur)
				}
			}

		case <-debounceCh:
			debounceCh = nil
			if pendingVersion >= 0 {
				w.fire(ctx, log, action, pendingVersion)
				pendingVersion = -1
			}
		}
	}
}

// WaitForVersion blocks until the watcher has observed and successfully
// processed (action returned nil) a version >= target, or ctx expires.
func (w *Watcher) WaitForVersion(ctx context.Context, target int64) error {
	// Fast path.
	if w.version.Load() >= target {
		return nil
	}

	done := ctx.Done()
	w.versionMu.Lock()
	defer w.versionMu.Unlock()

	for w.version.Load() < target {
		// Interruptible wait: spawn a goroutine that closes a channel on
		// context cancellation so we can select on both.
		ch := make(chan struct{})
		go func() {
			select {
			case <-done:
				w.versionCond.Broadcast()
			case <-ch:
			}
		}()

		w.versionCond.Wait()
		close(ch)

		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

func (w *Watcher) fire(ctx context.Context, log *slog.Logger, action func() error, ver int64) {
	log.Info("watch: reloading", "old_version", w.version.Load(), "new_version", ver)
	start := time.Now()
	if err := action(); err != nil {
		w.errors.Add(1)
		log.Error("watch: reload failed", "error", err, "version", ver)
		return
	}
	elapsed := time.Since(start)
	w.reloads.Add(1)
	w.reloadNs.Add(int64(elapsed))
	w.setVersion(ver)
	log.Info("watch: reload complete", "version", ver, "duration", elapsed)
}

func (w *Watcher) setVersion(v int64) {
	w.version.Store(v)
	w.versionCond.Broadcast()
}

// ---------- Built-in detectors ----------

// PragmaDataVersion uses PRAGMA data_version, which increments whenever
// another connection writes to the same database file. It detects cross-process
// and cross-connection mutations — ideal for hot reload.
func PragmaDataVersion(ctx context.Context, db *sql.DB) (int64, error) {
	var v int64
	err := db.QueryRowContext(ctx, "PRAGMA data_version").Scan(&v)
	return v, err
}

// PragmaUserVersion uses PRAGMA user_version, which is an application-controlled
// integer. Callers must bump it explicitly after writes. Useful when you want
// deterministic version numbers (e.g. for WaitForVersion).
func PragmaUserVersion(ctx context.Context, db *sql.DB) (int64, error) {
	var v int64
	err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}

// MaxColumnDetector returns a ChangeDetector that polls MAX(column) on a
// table. Handy for tables that use an auto-incrementing updated_at timestamp.
// Table and column names are quoted to prevent SQL injection.
func MaxColumnDetector(table, column string) ChangeDetector {
	query := "SELECT COALESCE(MAX(" + quoteIdent(column) + "), 0) FROM " + quoteIdent(table)
	return func(ctx context.Context, db *sql.DB) (int64, error) {
		var v int64
		err := db.QueryRowContext(ctx, query).Scan(&v)
		return v, err
	}
}

// quoteIdent wraps a SQL identifier in double quotes, escaping any embedded quotes.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

package dbsync

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Subscriber listens for incoming snapshot pushes over QUIC, verifies integrity,
// and performs an atomic swap of the local read-only database.
type Subscriber struct {
	dbPath     string
	listenAddr string
	tlsCfg     *tls.Config
	logger     *slog.Logger
	opts       options

	db     atomic.Pointer[sql.DB]
	mu     sync.Mutex
	onSwap []func()

	// Track current version for status reporting.
	lastVersion atomic.Int64
	lastHash    atomic.Value // string
	lastSwapAt  atomic.Int64 // unix timestamp of last successful swap
}

// NewSubscriber creates a Subscriber that listens on listenAddr for snapshot
// pushes and maintains a read-only database at dbPath.
func NewSubscriber(dbPath, listenAddr string, tlsCfg *tls.Config, opts ...Option) *Subscriber {
	o := defaultOptions()
	for _, fn := range opts {
		fn(&o)
	}
	s := &Subscriber{
		dbPath:     dbPath,
		listenAddr: listenAddr,
		tlsCfg:     tlsCfg,
		logger:     slog.Default(),
		opts:       o,
	}
	s.lastHash.Store("")

	// Open initial database if it exists.
	if _, err := os.Stat(dbPath); err == nil {
		if db, err := openReadOnly(o.driverName, dbPath); err == nil {
			s.db.Store(db)
			s.logger.Info("dbsync subscriber: loaded existing database", "path", dbPath)
		}
	}

	return s
}

// Start listens for incoming snapshots over QUIC. Blocks until ctx is cancelled.
func (s *Subscriber) Start(ctx context.Context) error {
	s.logger.Info("dbsync subscriber: starting", "listen", s.listenAddr, "db", s.dbPath)
	return ListenSnapshots(ctx, s.listenAddr, s.tlsCfg, func(meta SnapshotMeta, reader io.Reader) error {
		return s.handleSnapshot(meta, reader)
	})
}

// DB returns the current read-only database connection. May return nil if no
// snapshot has been received yet.
func (s *Subscriber) DB() *sql.DB {
	return s.db.Load()
}

// OnSwap registers a callback invoked after a successful database swap.
// Callbacks are called synchronously in registration order.
func (s *Subscriber) OnSwap(fn func()) {
	s.mu.Lock()
	s.onSwap = append(s.onSwap, fn)
	s.mu.Unlock()
}

// Ping reports whether the subscriber is healthy. It returns nil if a snapshot
// has been received and the database is accessible, or an error describing
// what is missing (no snapshot, DB unreachable).
func (s *Subscriber) Ping(ctx context.Context) error {
	db := s.db.Load()
	if db == nil {
		return errors.New("dbsync subscriber: no snapshot received yet")
	}
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("dbsync subscriber: database unreachable: %w", err)
	}
	return nil
}

// Version returns the version of the last received snapshot.
func (s *Subscriber) Version() int64 { return s.lastVersion.Load() }

// LastSwapAt returns the unix timestamp of the last successful database swap,
// or 0 if no swap has occurred yet.
func (s *Subscriber) LastSwapAt() int64 { return s.lastSwapAt.Load() }

// StaleSince returns how long since the last successful swap. Returns -1 if
// no swap has occurred yet.
func (s *Subscriber) StaleSince() time.Duration {
	ts := s.lastSwapAt.Load()
	if ts == 0 {
		return -1
	}
	return time.Since(time.Unix(ts, 0))
}

// Status returns a JSON-serializable status summary.
func (s *Subscriber) Status() map[string]any {
	swapAt := s.lastSwapAt.Load()
	st := map[string]any{
		"role":         "subscriber",
		"last_version": s.lastVersion.Load(),
		"last_hash":    s.lastHash.Load(),
		"has_db":       s.db.Load() != nil,
		"last_swap_at": swapAt,
	}
	if swapAt > 0 {
		st["age_seconds"] = int64(time.Since(time.Unix(swapAt, 0)).Seconds())
	}
	return st
}

// handleSnapshot receives a snapshot, validates it, and swaps the local DB.
func (s *Subscriber) handleSnapshot(meta SnapshotMeta, reader io.Reader) error {
	s.logger.Info("dbsync subscriber: receiving snapshot",
		"version", meta.Version, "size", meta.Size, "hash", meta.Hash[:16]+"...",
		"compressed", meta.Compressed)

	// Max-age validation: reject stale snapshots to prevent rollback attacks.
	if s.opts.maxAge > 0 && meta.Timestamp > 0 {
		age := time.Since(time.Unix(meta.Timestamp, 0))
		if age > s.opts.maxAge {
			return fmt.Errorf("snapshot too old: age %s exceeds max %s", age.Round(time.Second), s.opts.maxAge)
		}
	}

	// Ensure target directory exists.
	if err := os.MkdirAll(filepath.Dir(s.dbPath), 0755); err != nil {
		return fmt.Errorf("create db dir: %w", err)
	}

	// Write to temp file.
	tmpPath := s.dbPath + ".incoming"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}

	// Hash while writing.
	h := sha256.New()
	tee := io.TeeReader(reader, h)

	n, err := io.Copy(f, tee)
	f.Close()
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("write tmp: %w", err)
	}

	if n != meta.Size {
		os.Remove(tmpPath)
		return fmt.Errorf("size mismatch: got %d, expected %d", n, meta.Size)
	}

	gotHash := hex.EncodeToString(h.Sum(nil))
	if gotHash != meta.Hash {
		os.Remove(tmpPath)
		return fmt.Errorf("hash mismatch: got %s, expected %s", gotHash, meta.Hash)
	}

	s.logger.Info("dbsync subscriber: snapshot verified", "version", meta.Version)

	// Atomic swap: close old → rename → open new.
	s.mu.Lock()
	defer s.mu.Unlock()

	// Close old database.
	if old := s.db.Load(); old != nil {
		old.Close()
	}

	// Remove WAL/SHM files from previous database.
	os.Remove(s.dbPath + "-wal")
	os.Remove(s.dbPath + "-shm")

	// Rename tmp → target.
	if err := os.Rename(tmpPath, s.dbPath); err != nil {
		return fmt.Errorf("rename: %w", err)
	}

	// Open new database read-only.
	newDB, err := openReadOnly(s.opts.driverName, s.dbPath)
	if err != nil {
		return fmt.Errorf("open new db: %w", err)
	}

	s.db.Store(newDB)
	s.lastVersion.Store(meta.Version)
	s.lastHash.Store(meta.Hash)
	s.lastSwapAt.Store(time.Now().Unix())

	s.logger.Info("dbsync subscriber: database swapped", "version", meta.Version)

	// Fire swap callbacks.
	for _, fn := range s.onSwap {
		fn()
	}

	return nil
}

// openReadOnly opens a SQLite database in read-only mode with safe pragmas.
func openReadOnly(driverName, path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Close closes the current database connection.
func (s *Subscriber) Close() error {
	if db := s.db.Load(); db != nil {
		return db.Close()
	}
	return nil
}

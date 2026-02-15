package observability

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"time"

	"database/sql"
)

// RuntimeMetrics captures Go process health at a point in time.
type RuntimeMetrics struct {
	GoroutinesCount int
	MemoryAllocMB   float64
	MemorySysMB     float64
	GCCount         uint32
}

// CollectRuntimeMetrics reads current Go runtime stats (~10µs overhead).
func CollectRuntimeMetrics() RuntimeMetrics {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return RuntimeMetrics{
		GoroutinesCount: runtime.NumGoroutine(),
		MemoryAllocMB:   float64(mem.Alloc) / 1024 / 1024,
		MemorySysMB:     float64(mem.Sys) / 1024 / 1024,
		GCCount:         mem.NumGC,
	}
}

// HeartbeatWriter writes periodic liveness probes to the worker_heartbeats table.
type HeartbeatWriter struct {
	db         *sql.DB
	workerName string
	hostname   string
	workerPID  int
	interval   time.Duration
	stop       chan struct{}
	done       chan struct{}
}

// NewHeartbeatWriter creates a writer. Recommended interval: 15s.
func NewHeartbeatWriter(db *sql.DB, workerName string, interval time.Duration) *HeartbeatWriter {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	return &HeartbeatWriter{
		db:         db,
		workerName: workerName,
		hostname:   hostname,
		workerPID:  os.Getpid(),
		interval:   interval,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
}

// Start launches the heartbeat goroutine. It writes one heartbeat immediately,
// then repeats at the configured interval until Stop or context cancellation.
func (hw *HeartbeatWriter) Start(ctx context.Context) {
	go hw.loop(ctx)
}

// WriteHeartbeat writes a single heartbeat row with current runtime metrics.
func (hw *HeartbeatWriter) WriteHeartbeat() error {
	m := CollectRuntimeMetrics()
	_, err := hw.db.Exec(`
		INSERT INTO worker_heartbeats (
			worker_name, hostname, worker_pid, timestamp,
			goroutines_count, memory_alloc_mb, memory_sys_mb, gc_count
		) VALUES (?,?,?,?,?,?,?,?)`,
		hw.workerName, hw.hostname, hw.workerPID, time.Now().Unix(),
		m.GoroutinesCount, m.MemoryAllocMB, m.MemorySysMB, m.GCCount)
	if err != nil {
		return fmt.Errorf("insert heartbeat: %w", err)
	}
	return nil
}

// Stop signals the heartbeat goroutine to exit and waits for it.
func (hw *HeartbeatWriter) Stop() {
	close(hw.stop)
	<-hw.done
}

func (hw *HeartbeatWriter) loop(ctx context.Context) {
	defer close(hw.done)
	ticker := time.NewTicker(hw.interval)
	defer ticker.Stop()

	// Immediate first heartbeat.
	if err := hw.WriteHeartbeat(); err != nil {
		slog.Error("heartbeat write failed", "error", err, "worker", hw.workerName)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-hw.stop:
			return
		case <-ticker.C:
			if err := hw.WriteHeartbeat(); err != nil {
				slog.Error("heartbeat write failed", "error", err, "worker", hw.workerName)
			}
		}
	}
}

// HeartbeatStatus is the latest heartbeat for a worker, enriched with a
// staleness check so callers don't have to compute it themselves.
type HeartbeatStatus struct {
	WorkerName      string         `json:"worker_name"`
	Hostname        string         `json:"hostname"`
	PID             int            `json:"pid"`
	Timestamp       time.Time      `json:"timestamp"`
	GoroutinesCount int            `json:"goroutines_count"`
	MemoryAllocMB   float64        `json:"memory_alloc_mb"`
	MemorySysMB     float64        `json:"memory_sys_mb"`
	GCCount         int            `json:"gc_count"`
	Alive           bool           `json:"alive"`   // true if last beat is within staleness threshold
	StaleSince      *time.Duration `json:"stale_since,omitempty"` // how long past the threshold
}

// LatestHeartbeat returns the most recent heartbeat for the given worker.
// stalenessThreshold controls the alive/stale boundary (typically 3× the
// heartbeat interval). Returns nil, nil if no heartbeat has been recorded yet.
func LatestHeartbeat(ctx context.Context, db *sql.DB, workerName string, stalenessThreshold time.Duration) (*HeartbeatStatus, error) {
	row := db.QueryRowContext(ctx, `
		SELECT worker_name, hostname, worker_pid, timestamp,
		       goroutines_count, memory_alloc_mb, memory_sys_mb, gc_count
		FROM worker_heartbeats
		WHERE worker_name = ?
		ORDER BY timestamp DESC LIMIT 1`, workerName)

	var hs HeartbeatStatus
	var ts int64
	err := row.Scan(&hs.WorkerName, &hs.Hostname, &hs.PID, &ts,
		&hs.GoroutinesCount, &hs.MemoryAllocMB, &hs.MemorySysMB, &hs.GCCount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query latest heartbeat: %w", err)
	}

	hs.Timestamp = time.Unix(ts, 0)
	age := time.Since(hs.Timestamp)
	if age <= stalenessThreshold {
		hs.Alive = true
	} else {
		hs.Alive = false
		stale := age - stalenessThreshold
		hs.StaleSince = &stale
	}
	return &hs, nil
}

// CleanupHeartbeats deletes heartbeats older than retentionDays.
func CleanupHeartbeats(ctx context.Context, db *sql.DB, retentionDays int) (int64, error) {
	threshold := time.Now().AddDate(0, 0, -retentionDays).Unix()
	result, err := db.ExecContext(ctx, "DELETE FROM worker_heartbeats WHERE timestamp < ?", threshold)
	if err != nil {
		return 0, fmt.Errorf("cleanup heartbeats: %w", err)
	}
	return result.RowsAffected()
}

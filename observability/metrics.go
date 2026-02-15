// Package observability provides SQLite-native monitoring components that
// replace Prometheus, Loki, Consul health checks and Elasticsearch audit.
//
// Each component writes to a shared observability database (separate from the
// application database to avoid write contention). Call Init() on the shared
// *sql.DB first, then pass it to the individual constructors.
//
// All persistence is async and non-blocking: buffer overflow silently drops
// datapoints rather than applying backpressure to the application.
package observability

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Metric is a single timeseries datapoint.
type Metric struct {
	Name      string            // e.g. "cpu_usage_percent", "workflow_duration_ms"
	Timestamp time.Time
	Value     float64
	Labels    map[string]string // optional key/value pairs
	Unit      string            // "percent", "bytes", "milliseconds", "count"
}

// MetricsManager buffers metrics and flushes them to SQLite in batches.
type MetricsManager struct {
	db            *sql.DB
	bufferSize    int
	flushInterval time.Duration
	buffer        []*Metric
	mu            sync.Mutex
	stop          chan struct{}
	done          chan struct{}
}

// NewMetricsManager creates a manager that flushes metrics in batches.
// Recommended defaults: bufferSize=100, flushInterval=5s.
func NewMetricsManager(db *sql.DB, bufferSize int, flushInterval time.Duration) *MetricsManager {
	mm := &MetricsManager{
		db:            db,
		bufferSize:    bufferSize,
		flushInterval: flushInterval,
		buffer:        make([]*Metric, 0, bufferSize),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
	}
	go mm.flushLoop()
	return mm
}

// Record queues a metric for async persistence. Non-blocking.
func (mm *MetricsManager) Record(m *Metric) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.buffer = append(mm.buffer, m)
	if len(mm.buffer) >= mm.bufferSize {
		mm.flushLocked()
	}
}

// RecordSimple is a convenience helper for metrics without labels.
func (mm *MetricsManager) RecordSimple(name string, value float64, unit string) {
	mm.Record(&Metric{
		Name:      name,
		Timestamp: time.Now(),
		Value:     value,
		Unit:      unit,
	})
}

// Query retrieves metrics filtered by name, time range and limit.
// Pass empty metricName for all metrics. Nil time pointers mean unbounded.
func (mm *MetricsManager) Query(metricName string, startTime, endTime *time.Time, limit int) ([]*Metric, error) {
	q := "SELECT metric_name, timestamp, value, labels, unit FROM metrics_timeseries WHERE 1=1"
	args := make([]interface{}, 0, 4)

	if metricName != "" {
		q += " AND metric_name = ?"
		args = append(args, metricName)
	}
	if startTime != nil {
		q += " AND timestamp >= ?"
		args = append(args, startTime.Unix())
	}
	if endTime != nil {
		q += " AND timestamp <= ?"
		args = append(args, endTime.Unix())
	}
	q += " ORDER BY timestamp DESC"
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := mm.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("query metrics: %w", err)
	}
	defer rows.Close()

	var out []*Metric
	for rows.Next() {
		var name, unit string
		var ts int64
		var value float64
		var labelsJSON sql.NullString

		if err := rows.Scan(&name, &ts, &value, &labelsJSON, &unit); err != nil {
			return nil, fmt.Errorf("scan metric: %w", err)
		}
		m := &Metric{Name: name, Timestamp: time.Unix(ts, 0), Value: value, Unit: unit}
		if labelsJSON.Valid {
			var labels map[string]string
			if json.Unmarshal([]byte(labelsJSON.String), &labels) == nil {
				m.Labels = labels
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Cleanup deletes metrics older than retentionDays and returns the count removed.
func (mm *MetricsManager) Cleanup(ctx context.Context, retentionDays int) (int64, error) {
	threshold := time.Now().AddDate(0, 0, -retentionDays).Unix()
	result, err := mm.db.ExecContext(ctx, "DELETE FROM metrics_timeseries WHERE timestamp < ?", threshold)
	if err != nil {
		return 0, fmt.Errorf("cleanup metrics: %w", err)
	}
	return result.RowsAffected()
}

// Close flushes remaining metrics and stops the background goroutine.
func (mm *MetricsManager) Close() error {
	close(mm.stop)
	<-mm.done
	return nil
}

func (mm *MetricsManager) flushLoop() {
	defer close(mm.done)
	ticker := time.NewTicker(mm.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-mm.stop:
			mm.mu.Lock()
			mm.flushLocked()
			mm.mu.Unlock()
			return
		case <-ticker.C:
			mm.mu.Lock()
			mm.flushLocked()
			mm.mu.Unlock()
		}
	}
}

func (mm *MetricsManager) flushLocked() {
	if len(mm.buffer) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tx, err := mm.db.BeginTx(ctx, nil)
	if err != nil {
		slog.Error("observability metrics: begin tx", "error", err)
		return
	}

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO metrics_timeseries (metric_name, timestamp, value, labels, unit) VALUES (?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		slog.Error("observability metrics: prepare", "error", err)
		return
	}
	defer stmt.Close()

	for _, m := range mm.buffer {
		var labelsJSON sql.NullString
		if len(m.Labels) > 0 {
			if b, err := json.Marshal(m.Labels); err == nil {
				labelsJSON = sql.NullString{String: string(b), Valid: true}
			}
		}
		if _, err := stmt.ExecContext(ctx, m.Name, m.Timestamp.Unix(), m.Value, labelsJSON, m.Unit); err != nil {
			slog.Error("observability metrics: insert", "error", err, "metric", m.Name)
		}
	}

	if err := tx.Commit(); err != nil {
		slog.Error("observability metrics: commit", "error", err)
	}
	mm.buffer = mm.buffer[:0]
}

// Standard metric name constants.
const (
	MetricCPUUsagePercent    = "cpu_usage_percent"
	MetricMemoryUsedBytes    = "memory_used_bytes"
	MetricMemoryAllocMB      = "memory_alloc_mb"
	MetricGoroutinesCount    = "goroutines_count"
	MetricGCCount            = "gc_count"
	MetricWorkflowDurationMs = "workflow_duration_ms"
	MetricTaskProcessedCount = "task_processed_count"
)

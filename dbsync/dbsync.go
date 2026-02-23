// Package dbsync provides database replication from a back-office (BO) to one
// or more front-office (FO) instances. The BO produces filtered SQLite snapshots
// and pushes them over QUIC. Each FO receives the snapshot, validates its
// integrity, and swaps the local read-only database atomically.
//
// The package integrates with hazyhaar_pkg/connectivity for dynamic target
// management and hazyhaar_pkg/watch for change detection.
package dbsync

import (
	"context"
	"database/sql"
	"time"
)

// ALPN and wire-format constants.
const (
	// ALPNProtocol identifies dbsync streams over QUIC, distinct from MCP.
	ALPNProtocol = "horos-dbsync-v1"

	// MagicBytes are sent at the start of every snapshot stream for framing.
	MagicBytes = "SYN1"

	// MaxSnapshotSize is a safety limit (512 MB).
	MaxSnapshotSize = 512 * 1024 * 1024
)

// FilterSpec defines which tables and columns are included in the public
// snapshot pushed to FO instances. Anything not listed is excluded.
type FilterSpec struct {
	// FullTables are copied integrally without modification.
	FullTables []string

	// FilteredTables maps table name → WHERE clause. Only rows matching
	// the clause are included.
	FilteredTables map[string]string

	// PartialTables maps table name → column selection + optional WHERE.
	// Columns not listed are dropped (set to their zero value).
	PartialTables map[string]PartialTable
}

// PartialTable describes a table where only selected columns are published
// and an optional WHERE clause restricts rows.
type PartialTable struct {
	Columns []string
	Where   string
}

// SnapshotMeta is sent as the first message on the QUIC stream before the
// raw database bytes follow.
type SnapshotMeta struct {
	Version    int64  `json:"version"`
	Hash       string `json:"hash"`       // SHA-256 hex of uncompressed data
	Size       int64  `json:"size"`        // uncompressed file size in bytes
	Timestamp  int64  `json:"timestamp"`   // unix epoch seconds
	Compressed bool   `json:"compressed"`  // true if payload is gzip-compressed
}

// TargetProvider returns the list of sync targets that a Publisher should push
// snapshots to. Implementations range from a static list to a database-backed
// route table.
type TargetProvider interface {
	Targets(ctx context.Context) ([]Target, error)
}

// Target describes a single FO endpoint that receives snapshots.
type Target struct {
	Name     string // identifier, e.g. "fo-1"
	Strategy string // "dbsync" or "noop"
	Endpoint string // "ip:port" for QUIC dial
}

// StaticTargetProvider returns a fixed list of targets.
type StaticTargetProvider struct {
	targets []Target
}

// NewStaticTargetProvider creates a TargetProvider from a fixed list of targets.
func NewStaticTargetProvider(targets ...Target) *StaticTargetProvider {
	return &StaticTargetProvider{targets: targets}
}

// Targets returns the static list.
func (p *StaticTargetProvider) Targets(_ context.Context) ([]Target, error) {
	return p.targets, nil
}

// RoutesTargetProvider reads dbsync targets from a connectivity routes table.
type RoutesTargetProvider struct {
	db *sql.DB
}

// NewRoutesTargetProvider creates a TargetProvider backed by the connectivity
// routes table in db. It queries rows where strategy IN ('dbsync', 'noop')
// AND service_name LIKE 'dbsync:%'.
func NewRoutesTargetProvider(db *sql.DB) *RoutesTargetProvider {
	return &RoutesTargetProvider{db: db}
}

// Targets queries the routes table for dbsync endpoints.
func (p *RoutesTargetProvider) Targets(ctx context.Context) ([]Target, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT service_name, strategy, COALESCE(endpoint, '') FROM routes WHERE strategy IN ('dbsync', 'noop') AND service_name LIKE 'dbsync:%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []Target
	for rows.Next() {
		var t Target
		if err := rows.Scan(&t.Name, &t.Strategy, &t.Endpoint); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, rows.Err()
}

// Option configures Publisher or Subscriber.
type Option func(*options)

type options struct {
	watchInterval time.Duration
	watchDebounce time.Duration
	compress      bool          // gzip snapshot before push
	maxAge        time.Duration // reject snapshots older than this (0 = no limit)
	driverName    string        // SQL driver name for opening databases (default "sqlite")
}

func defaultOptions() options {
	return options{
		watchInterval: 200 * time.Millisecond,
		watchDebounce: 200 * time.Millisecond,
		driverName:    "sqlite",
	}
}

// WithWatchInterval sets the database polling interval.
func WithWatchInterval(d time.Duration) Option {
	return func(o *options) { o.watchInterval = d }
}

// WithWatchDebounce sets the debounce window after a change is detected.
func WithWatchDebounce(d time.Duration) Option {
	return func(o *options) { o.watchDebounce = d }
}

// WithCompression enables gzip compression of snapshot payloads before push.
// Typically reduces transfer size by 60-70% for SQLite databases.
func WithCompression() Option {
	return func(o *options) { o.compress = true }
}

// WithMaxAge sets the maximum acceptable age for incoming snapshots.
// The subscriber rejects snapshots whose timestamp is older than this duration,
// protecting against rollback/replay attacks.
func WithMaxAge(d time.Duration) Option {
	return func(o *options) { o.maxAge = d }
}

// WithDriverName sets the SQL driver name used when opening databases.
// Defaults to "sqlite". Use "sqlite-trace" to enable SQL tracing.
func WithDriverName(name string) Option {
	return func(o *options) { o.driverName = name }
}

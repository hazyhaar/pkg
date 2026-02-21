// Package dbsync provides database replication from a back-office (BO) to one
// or more front-office (FO) instances. The BO produces filtered SQLite snapshots
// and pushes them over QUIC. Each FO receives the snapshot, validates its
// integrity, and swaps the local read-only database atomically.
//
// The package integrates with hazyhaar_pkg/connectivity for dynamic target
// management and hazyhaar_pkg/watch for change detection.
package dbsync

import "time"

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
	Version   int64  `json:"version"`
	Hash      string `json:"hash"`      // SHA-256 hex
	Size      int64  `json:"size"`       // file size in bytes
	Timestamp int64  `json:"timestamp"`  // unix epoch seconds
}

// Option configures Publisher or Subscriber.
type Option func(*options)

type options struct {
	watchInterval time.Duration
	watchDebounce time.Duration
}

func defaultOptions() options {
	return options{
		watchInterval: 200 * time.Millisecond,
		watchDebounce: 200 * time.Millisecond,
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

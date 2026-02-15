package controlplane

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/hazyhaar/pkg/sqlitedb"
)

// ControlPlane is the central administration interface for hpx.
// It wraps a SQLite database containing the 12 control plane tables
// and provides typed CRUD operations for each.
type ControlPlane struct {
	db     *sql.DB
	logger *slog.Logger
}

// Option configures a ControlPlane.
type Option func(*ControlPlane)

// WithLogger sets a custom logger for the control plane.
func WithLogger(l *slog.Logger) Option {
	return func(cp *ControlPlane) { cp.logger = l }
}

// New creates a ControlPlane backed by the given database.
// Call Init to apply the schema, or use Migrate for incremental updates.
func New(db *sql.DB, opts ...Option) *ControlPlane {
	cp := &ControlPlane{
		db:     db,
		logger: slog.Default(),
	}
	for _, o := range opts {
		o(cp)
	}
	return cp
}

// Open creates a new ControlPlane with a freshly opened database.
// This is a convenience that combines sqlitedb.Open + New + Init.
func Open(ctx context.Context, dsn string, opts ...Option) (*ControlPlane, error) {
	db, err := sqlitedb.Open(dsn)
	if err != nil {
		return nil, fmt.Errorf("controlplane: open: %w", err)
	}
	cp := New(db, opts...)
	if err := cp.Init(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return cp, nil
}

// Init applies the full control plane schema to the database.
// Safe to call multiple times (all statements use IF NOT EXISTS).
func (cp *ControlPlane) Init(ctx context.Context) error {
	if _, err := cp.db.ExecContext(ctx, Schema); err != nil {
		return fmt.Errorf("controlplane: init schema: %w", err)
	}
	cp.logger.Info("control plane initialized")
	return nil
}

// DB returns the underlying database handle.
func (cp *ControlPlane) DB() *sql.DB { return cp.db }

// Close closes the underlying database connection.
func (cp *ControlPlane) Close() error { return cp.db.Close() }

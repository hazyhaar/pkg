package sqlitedb

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"time"
)

// Migration represents a single schema migration step.
type Migration struct {
	Version     int
	Description string
	SQL         string
}

// migrationsTable creates the schema_migrations tracking table.
const migrationsTable = `
CREATE TABLE IF NOT EXISTS hpx_schema_migrations (
    version     INTEGER PRIMARY KEY,
    description TEXT NOT NULL,
    applied_at  INTEGER NOT NULL
);`

// Migrate applies pending migrations in order. Migrations already applied
// (tracked in hpx_schema_migrations) are skipped. Each migration runs in
// its own transaction for atomicity.
func Migrate(ctx context.Context, db *sql.DB, migrations []Migration) error {
	if _, err := db.ExecContext(ctx, migrationsTable); err != nil {
		return fmt.Errorf("sqlitedb: create migrations table: %w", err)
	}

	// Sort by version.
	sorted := make([]Migration, len(migrations))
	copy(sorted, migrations)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Version < sorted[j].Version
	})

	for _, m := range sorted {
		var exists int
		err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM hpx_schema_migrations WHERE version = ?`, m.Version).Scan(&exists)
		if err != nil {
			return fmt.Errorf("sqlitedb: check migration %d: %w", m.Version, err)
		}
		if exists > 0 {
			continue
		}

		slog.Info("applying migration",
			"version", m.Version,
			"description", m.Description)

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("sqlitedb: begin migration %d: %w", m.Version, err)
		}

		if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
			tx.Rollback()
			return fmt.Errorf("sqlitedb: migration %d (%s): %w", m.Version, m.Description, err)
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO hpx_schema_migrations (version, description, applied_at) VALUES (?, ?, ?)`,
			m.Version, m.Description, time.Now().Unix()); err != nil {
			tx.Rollback()
			return fmt.Errorf("sqlitedb: record migration %d: %w", m.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("sqlitedb: commit migration %d: %w", m.Version, err)
		}

		slog.Info("migration applied",
			"version", m.Version,
			"description", m.Description)
	}

	return nil
}

// CurrentVersion returns the highest applied migration version, or 0
// if no migrations have been applied.
func CurrentVersion(ctx context.Context, db *sql.DB) (int, error) {
	// Ensure table exists first.
	if _, err := db.ExecContext(ctx, migrationsTable); err != nil {
		return 0, err
	}
	var ver sql.NullInt64
	err := db.QueryRowContext(ctx,
		`SELECT MAX(version) FROM hpx_schema_migrations`).Scan(&ver)
	if err != nil {
		return 0, err
	}
	if ver.Valid {
		return int(ver.Int64), nil
	}
	return 0, nil
}

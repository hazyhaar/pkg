package connectivity

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// Watch polls PRAGMA data_version on the database at the given interval.
// When the version changes (meaning any write occurred), it triggers a Reload.
//
// data_version is auto-incremented by SQLite on any write — no triggers needed.
// This is the same proven pattern used by the mcprt tool registry.
//
// Watch blocks until ctx is cancelled. Run it in a goroutine:
//
//	go router.Watch(ctx, db, 200*time.Millisecond)
func (r *Router) Watch(ctx context.Context, db *sql.DB, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastVersion int64

	// Initial load.
	if err := r.Reload(ctx, db); err != nil {
		r.logger.Error("connectivity: initial reload failed", "error", err)
	}
	db.QueryRow("PRAGMA data_version").Scan(&lastVersion)

	r.logger.Info("connectivity watcher started", "interval", interval)

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("connectivity watcher stopped")
			return
		case <-ticker.C:
			var ver int64
			if err := db.QueryRow("PRAGMA data_version").Scan(&ver); err != nil {
				slog.Warn("connectivity: data_version poll failed", "error", err)
				continue
			}
			if ver != lastVersion {
				r.logger.Info("connectivity: change detected, reloading",
					"old_version", lastVersion, "new_version", ver)
				if err := r.Reload(ctx, db); err != nil {
					r.logger.Error("connectivity: reload failed", "error", err)
				}
				lastVersion = ver
			}
		}
	}
}

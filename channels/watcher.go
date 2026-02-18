package channels

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// Watch polls PRAGMA data_version on the database at the given interval.
// When the version changes (meaning any write to the channels table or any
// other table in the same database occurred), it triggers a Reload.
//
// data_version is auto-incremented by SQLite on any write — no triggers needed.
// This is the same proven pattern used by connectivity.Router.Watch and the
// mcprt tool registry.
//
// Watch blocks until ctx is cancelled. Run it in a goroutine:
//
//	go dispatcher.Watch(ctx, db, 200*time.Millisecond)
func (d *Dispatcher) Watch(ctx context.Context, db *sql.DB, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastVersion int64

	// Initial load.
	if err := d.Reload(ctx, db); err != nil {
		d.logger.Error("channels: initial reload failed", "error", err)
	}
	db.QueryRow("PRAGMA data_version").Scan(&lastVersion)

	d.logger.Info("channels watcher started", "interval", interval)

	for {
		select {
		case <-ctx.Done():
			d.logger.Info("channels watcher stopped")
			return
		case <-ticker.C:
			var ver int64
			if err := db.QueryRow("PRAGMA data_version").Scan(&ver); err != nil {
				slog.Warn("channels: data_version poll failed", "error", err)
				continue
			}
			if ver != lastVersion {
				d.logger.Info("channels: change detected, reloading",
					"old_version", lastVersion, "new_version", ver)
				if err := d.Reload(ctx, db); err != nil {
					d.logger.Error("channels: reload failed", "error", err)
				}
				lastVersion = ver
			}
		}
	}
}

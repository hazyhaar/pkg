package controlplane

import (
	"context"
	"log/slog"
	"time"
)

// ChangeHandler is called when the watcher detects a database change.
type ChangeHandler func(ctx context.Context) error

// Watch polls PRAGMA data_version at the given interval and calls the
// handler when a change is detected. This is the proven hot-reload
// mechanism: SQLite auto-increments data_version on any write from
// another connection, so we detect config changes without triggers.
//
// Watch blocks until ctx is cancelled. Run it in a goroutine:
//
//	go cp.Watch(ctx, 200*time.Millisecond, reloadRoutes)
func (cp *ControlPlane) Watch(ctx context.Context, interval time.Duration, handler ChangeHandler) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastVersion int64
	cp.db.QueryRow("PRAGMA data_version").Scan(&lastVersion)

	cp.logger.Info("controlplane watcher started", "interval", interval)

	for {
		select {
		case <-ctx.Done():
			cp.logger.Info("controlplane watcher stopped")
			return
		case <-ticker.C:
			var ver int64
			if err := cp.db.QueryRow("PRAGMA data_version").Scan(&ver); err != nil {
				slog.Warn("controlplane: data_version poll failed", "error", err)
				continue
			}
			if ver != lastVersion {
				cp.logger.Info("controlplane: change detected",
					"old_version", lastVersion, "new_version", ver)
				if err := handler(ctx); err != nil {
					cp.logger.Error("controlplane: change handler failed", "error", err)
				}
				lastVersion = ver
			}
		}
	}
}

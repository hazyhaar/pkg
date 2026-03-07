// CLAUDE:SUMMARY Dedicated JSON-lines shard activity log — zero SQLite, zero loop risk, filterable by dossier_id.
// CLAUDE:DEPENDS github.com/hazyhaar/pkg/kit
// CLAUDE:EXPORTS Init, Log, Hook, Sync, ShardHook, Op constants
package shardlog

import (
	"context"
	"log/slog"
	"os"
	"sync"

	"github.com/hazyhaar/pkg/kit"
)

// Op constants for shard lifecycle events.
const (
	OpOpened     = "shard.opened"
	OpResolved   = "shard.resolved"
	OpEvicted    = "shard.evicted"
	OpCreated    = "shard.created"
	OpDeleted    = "shard.deleted"
	OpSearch     = "shard.search"
	OpInsert     = "shard.insert"
	OpEmbed      = "shard.embed"
	OpCheckpoint = "shard.checkpoint"
	OpReload     = "shard.reload"
	OpError      = "shard.error"
)

// ShardHook is a callback injected into usertenant/dbopen (no shardlog import needed).
type ShardHook = func(op string, dossierID string, attrs ...slog.Attr)

var (
	mu     sync.RWMutex
	logger *slog.Logger
	file   *os.File
)

// Init opens the log file and creates a dedicated slog.Logger writing JSON lines.
// It creates parent directories if needed. Safe to call multiple times (closes previous file).
// CLAUDE:WARN Takes mu.Lock, closes previous file. All concurrent Log calls block during Init.
func Init(path string) error {
	mu.Lock()
	defer mu.Unlock()

	if file != nil {
		file.Close()
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	file = f
	logger = slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	return nil
}

// Log writes a shard event enriched with trace_id and user_id from context.
func Log(ctx context.Context, op, dossierID string, attrs ...slog.Attr) {
	mu.RLock()
	l := logger
	mu.RUnlock()

	if l == nil {
		return
	}

	base := []slog.Attr{
		slog.String("op", op),
		slog.String("dossier_id", dossierID),
	}

	if tid := kit.GetTraceID(ctx); tid != "" {
		base = append(base, slog.String("trace_id", tid))
	}
	if uid := kit.GetUserID(ctx); uid != "" {
		base = append(base, slog.String("user_id", uid))
	}

	all := append(base, attrs...)
	l.LogAttrs(ctx, slog.LevelInfo, op, all...)
}

// Hook returns a ShardHook closure suitable for injection into usertenant or dbopen.
// The hook calls Log with context.Background().
func Hook() ShardHook {
	return func(op string, dossierID string, attrs ...slog.Attr) {
		Log(context.Background(), op, dossierID, attrs...)
	}
}

// Sync flushes the underlying file.
func Sync() {
	mu.RLock()
	f := file
	mu.RUnlock()

	if f != nil {
		if err := f.Sync(); err != nil {
			slog.Warn("shardlog: sync failed", "error", err)
		}
	}
}

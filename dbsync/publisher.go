package dbsync

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/hazyhaar/pkg/watch"
)

// Publisher watches a source database for changes, produces filtered snapshots,
// and pushes them to all FO subscribers returned by the TargetProvider.
type Publisher struct {
	db       *sql.DB
	targets  TargetProvider
	filter   FilterSpec
	savePath string
	tlsCfg   *tls.Config
	logger   *slog.Logger
	opts     options

	// lastMeta tracks the most recently produced snapshot.
	mu       sync.RWMutex
	lastMeta *SnapshotMeta
	lastHash string // dedup: skip push when hash unchanged
}

// NewPublisher creates a Publisher that resolves push targets via the given
// TargetProvider. For services that push to a fixed set of FOs, use
// NewStaticTargetProvider. For dynamic routing via a connectivity routes DB,
// use NewRoutesTargetProvider.
//
// Parameters:
//   - db: the source (BO) database to snapshot
//   - targets: provides the list of FO endpoints to push to
//   - filter: defines which tables/columns are included
//   - savePath: path for the local "save chaude" snapshot file
//   - tlsCfg: TLS config for QUIC push (use SyncClientTLSConfig for dev)
func NewPublisher(db *sql.DB, targets TargetProvider, filter FilterSpec, savePath string, tlsCfg *tls.Config, opts ...Option) *Publisher {
	o := defaultOptions()
	for _, fn := range opts {
		fn(&o)
	}
	return &Publisher{
		db:       db,
		targets:  targets,
		filter:   filter,
		savePath: savePath,
		tlsCfg:   tlsCfg,
		logger:   slog.Default(),
		opts:     o,
	}
}

// NewPublisherWithRoutesDB creates a Publisher that reads push targets from a
// connectivity routes table. This is the original constructor signature kept
// for backward compatibility with repvow and other existing callers.
func NewPublisherWithRoutesDB(db, routesDB *sql.DB, filter FilterSpec, savePath string, tlsCfg *tls.Config, opts ...Option) *Publisher {
	return NewPublisher(db, NewRoutesTargetProvider(routesDB), filter, savePath, tlsCfg, opts...)
}

// Start watches the source database for changes and pushes snapshots to FOs.
// Blocks until ctx is cancelled.
func (p *Publisher) Start(ctx context.Context) error {
	w := watch.New(p.db, watch.Options{
		Interval: p.opts.watchInterval,
		Debounce: p.opts.watchDebounce,
	})

	w.OnChange(ctx, func() error {
		return p.publish(ctx)
	})
	return nil
}

// Ping verifies that the source database is accessible.
func (p *Publisher) Ping(ctx context.Context) error {
	return p.db.PingContext(ctx)
}

// LastMeta returns the metadata of the most recently produced snapshot.
func (p *Publisher) LastMeta() *SnapshotMeta {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastMeta
}

// publish produces a snapshot and pushes it to all dbsync targets.
func (p *Publisher) publish(ctx context.Context) error {
	// Step 1: Produce filtered snapshot.
	meta, err := ProduceSnapshot(p.db, p.savePath, p.filter)
	if err != nil {
		p.logger.Error("dbsync: produce snapshot failed", "error", err)
		return fmt.Errorf("produce snapshot: %w", err)
	}
	meta.Compressed = p.opts.compress

	p.mu.Lock()
	prevHash := p.lastHash
	p.lastMeta = meta
	p.lastHash = meta.Hash
	p.mu.Unlock()

	if meta.Hash == prevHash {
		p.logger.Debug("dbsync: snapshot unchanged, skipping push", "hash", meta.Hash[:16]+"...")
		return nil
	}

	p.logger.Info("dbsync: snapshot produced",
		"version", meta.Version,
		"size", meta.Size,
		"hash", meta.Hash[:16]+"...",
	)

	// Step 2: Read dbsync targets from provider.
	targets, err := p.targets.Targets(ctx)
	if err != nil {
		p.logger.Error("dbsync: load targets failed", "error", err)
		return fmt.Errorf("load targets: %w", err)
	}

	if len(targets) == 0 {
		p.logger.Debug("dbsync: no targets configured")
		return nil
	}

	// Step 3: Push to all targets in parallel.
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(target Target) {
			defer wg.Done()

			if target.Strategy == "noop" {
				p.logger.Debug("dbsync: skipping noop target", "service", target.Name)
				return
			}

			p.logger.Info("dbsync: pushing to target",
				"service", target.Name, "endpoint", target.Endpoint)

			if err := PushSnapshot(ctx, target.Endpoint, p.tlsCfg, *meta, p.savePath); err != nil {
				p.logger.Error("dbsync: push failed",
					"service", target.Name, "endpoint", target.Endpoint, "error", err)
			} else {
				p.logger.Info("dbsync: push succeeded", "service", target.Name)
			}
		}(t)
	}
	wg.Wait()

	return nil
}

// Status returns a JSON-serializable status summary.
func (p *Publisher) Status() map[string]any {
	p.mu.RLock()
	defer p.mu.RUnlock()

	status := map[string]any{"role": "publisher"}
	if p.lastMeta != nil {
		b, _ := json.Marshal(p.lastMeta)
		var m map[string]any
		json.Unmarshal(b, &m)
		status["last_snapshot"] = m
	}
	return status
}

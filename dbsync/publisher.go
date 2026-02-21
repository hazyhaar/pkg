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
// and pushes them to all FO subscribers registered in the routes table.
type Publisher struct {
	db       *sql.DB
	routesDB *sql.DB
	filter   FilterSpec
	savePath string
	tlsCfg   *tls.Config
	logger   *slog.Logger
	opts     options

	// lastMeta tracks the most recently produced snapshot.
	mu       sync.RWMutex
	lastMeta *SnapshotMeta
}

// NewPublisher creates a Publisher.
//
// Parameters:
//   - db: the source (BO) database to snapshot
//   - routesDB: database containing the connectivity routes table
//   - filter: defines which tables/columns are included
//   - savePath: path for the local "save chaude" snapshot file
//   - tlsCfg: TLS config for QUIC push (use SyncClientTLSConfig for dev)
func NewPublisher(db, routesDB *sql.DB, filter FilterSpec, savePath string, tlsCfg *tls.Config, opts ...Option) *Publisher {
	o := defaultOptions()
	for _, fn := range opts {
		fn(&o)
	}
	return &Publisher{
		db:       db,
		routesDB: routesDB,
		filter:   filter,
		savePath: savePath,
		tlsCfg:   tlsCfg,
		logger:   slog.Default(),
		opts:     o,
	}
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

	p.mu.Lock()
	p.lastMeta = meta
	p.mu.Unlock()

	p.logger.Info("dbsync: snapshot produced",
		"version", meta.Version,
		"size", meta.Size,
		"hash", meta.Hash[:16]+"...",
	)

	// Step 2: Read dbsync targets from routes table.
	targets, err := p.loadTargets(ctx)
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
		go func(target syncTarget) {
			defer wg.Done()

			if target.strategy == "noop" {
				p.logger.Debug("dbsync: skipping noop target", "service", target.name)
				return
			}

			p.logger.Info("dbsync: pushing to target",
				"service", target.name, "endpoint", target.endpoint)

			if err := PushSnapshot(ctx, target.endpoint, p.tlsCfg, *meta, p.savePath); err != nil {
				p.logger.Error("dbsync: push failed",
					"service", target.name, "endpoint", target.endpoint, "error", err)
			} else {
				p.logger.Info("dbsync: push succeeded", "service", target.name)
			}
		}(t)
	}
	wg.Wait()

	return nil
}

type syncTarget struct {
	name     string
	strategy string
	endpoint string
}

// loadTargets reads dbsync routes from the connectivity routes table.
func (p *Publisher) loadTargets(ctx context.Context) ([]syncTarget, error) {
	rows, err := p.routesDB.QueryContext(ctx,
		`SELECT service_name, strategy, COALESCE(endpoint, '') FROM routes WHERE strategy IN ('dbsync', 'noop') AND service_name LIKE 'dbsync:%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var targets []syncTarget
	for rows.Next() {
		var t syncTarget
		if err := rows.Scan(&t.name, &t.strategy, &t.endpoint); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, rows.Err()
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

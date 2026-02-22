// Package vtq implements a Visibility Timeout Queue backed by SQLite.
//
// Rows in the queue are invisible to consumers for a configurable duration
// after being claimed. If the holder processes the row successfully it deletes
// (or acks) it. If the holder crashes or exceeds the timeout the row
// reappears automatically — another instance can claim it.
//
// This single primitive covers three distributed patterns through calibration:
//
//   - 1 row, N instances  → leader election
//   - N rows, N instances → work distribution (TDMA)
//   - visibility < processing time under load → elastic overflow
//
// The queue is pure SQLite — no external broker, no cloud dependency.
//
// Expected schema (created automatically by EnsureTable):
//
//	CREATE TABLE IF NOT EXISTS vtq_jobs (
//	    id          TEXT PRIMARY KEY,
//	    queue       TEXT NOT NULL DEFAULT '',
//	    payload     BLOB,
//	    visible_at  INTEGER NOT NULL DEFAULT 0,  -- milliseconds since epoch
//	    created_at  INTEGER NOT NULL,             -- milliseconds since epoch
//	    attempts    INTEGER NOT NULL DEFAULT 0
//	);
//	CREATE INDEX IF NOT EXISTS idx_vtq_visible ON vtq_jobs (queue, visible_at);
package vtq

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Job is a row in the queue.
type Job struct {
	ID        string
	Queue     string
	Payload   []byte
	VisibleAt time.Time
	CreatedAt time.Time
	Attempts  int
}

// Options configures queue behaviour.
type Options struct {
	// Queue is the logical queue name. Multiple queues can coexist in the
	// same table. Default: "" (empty string — the default queue).
	Queue string
	// Visibility is how long a claimed job stays invisible. Default: 30s.
	Visibility time.Duration
	// PollInterval is the delay between claim attempts in the Run loop.
	// Default: 1s.
	PollInterval time.Duration
	// MaxAttempts limits how many times a job can be redelivered before
	// being discarded. 0 means unlimited. Default: 0.
	MaxAttempts int
	// Logger overrides the default slog logger.
	Logger *slog.Logger
}

func (o *Options) defaults() {
	if o.Visibility <= 0 {
		o.Visibility = 30 * time.Second
	}
	if o.PollInterval <= 0 {
		o.PollInterval = time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
}

// Q is the queue handle.
type Q struct {
	db   *sql.DB
	opts Options
}

// New creates a queue handle. Call EnsureTable once at startup, then Publish
// and Claim (or Run) as needed.
func New(db *sql.DB, opts Options) *Q {
	opts.defaults()
	return &Q{db: db, opts: opts}
}

// EnsureTable creates the vtq_jobs table and index if they don't exist.
func (q *Q) EnsureTable(ctx context.Context) error {
	_, err := q.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS vtq_jobs (
			id          TEXT PRIMARY KEY,
			queue       TEXT NOT NULL DEFAULT '',
			payload     BLOB,
			visible_at  INTEGER NOT NULL DEFAULT 0,
			created_at  INTEGER NOT NULL,
			attempts    INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_vtq_visible ON vtq_jobs (queue, visible_at);
	`)
	return err
}

// Publish inserts a job that is immediately visible.
func (q *Q) Publish(ctx context.Context, id string, payload []byte) error {
	now := time.Now().UnixMilli()
	_, err := q.db.ExecContext(ctx,
		`INSERT INTO vtq_jobs (id, queue, payload, visible_at, created_at) VALUES (?,?,?,?,?)`,
		id, q.opts.Queue, payload, now, now,
	)
	return err
}

// Claim atomically picks the oldest visible job, marks it invisible for the
// configured visibility duration, and returns it. Returns nil, nil if no job
// is available.
func (q *Q) Claim(ctx context.Context) (*Job, error) {
	now := time.Now()
	hideUntil := now.Add(q.opts.Visibility).UnixMilli()

	row := q.db.QueryRowContext(ctx, `
		UPDATE vtq_jobs
		SET visible_at = ?, attempts = attempts + 1
		WHERE id = (
			SELECT id FROM vtq_jobs
			WHERE queue = ? AND visible_at <= ?
			ORDER BY visible_at ASC
			LIMIT 1
		)
		RETURNING id, queue, payload, visible_at, created_at, attempts`,
		hideUntil, q.opts.Queue, now.UnixMilli(),
	)

	var j Job
	var visAt, creAt int64
	err := row.Scan(&j.ID, &j.Queue, &j.Payload, &visAt, &creAt, &j.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.VisibleAt = time.UnixMilli(visAt)
	j.CreatedAt = time.UnixMilli(creAt)
	return &j, nil
}

// Ack deletes a successfully processed job.
func (q *Q) Ack(ctx context.Context, id string) error {
	_, err := q.db.ExecContext(ctx,
		`DELETE FROM vtq_jobs WHERE id = ? AND queue = ?`, id, q.opts.Queue,
	)
	return err
}

// Nack makes a job immediately visible again so another consumer can pick it up.
func (q *Q) Nack(ctx context.Context, id string) error {
	_, err := q.db.ExecContext(ctx,
		`UPDATE vtq_jobs SET visible_at = 0 WHERE id = ? AND queue = ?`, id, q.opts.Queue,
	)
	return err
}

// Extend pushes the visibility timeout forward for a job that needs more
// processing time (heartbeat pattern).
func (q *Q) Extend(ctx context.Context, id string, extra time.Duration) error {
	hideUntil := time.Now().Add(extra).UnixMilli()
	_, err := q.db.ExecContext(ctx,
		`UPDATE vtq_jobs SET visible_at = ? WHERE id = ? AND queue = ?`,
		hideUntil, id, q.opts.Queue,
	)
	return err
}

// Purge deletes all jobs in the queue.
func (q *Q) Purge(ctx context.Context) error {
	_, err := q.db.ExecContext(ctx,
		`DELETE FROM vtq_jobs WHERE queue = ?`, q.opts.Queue,
	)
	return err
}

// Len returns the total number of jobs (visible + invisible) in the queue.
func (q *Q) Len(ctx context.Context) (int, error) {
	var n int
	err := q.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM vtq_jobs WHERE queue = ?`, q.opts.Queue,
	).Scan(&n)
	return n, err
}

// Handler processes a claimed job. Return nil to ack, non-nil to nack.
type Handler func(ctx context.Context, job *Job) error

// Run polls for visible jobs and calls handler for each one. It blocks until
// ctx is cancelled.
func (q *Q) Run(ctx context.Context, handler Handler) {
	log := q.opts.Logger
	log.Info("vtq: consumer started", "queue", q.opts.Queue, "visibility", q.opts.Visibility, "poll", q.opts.PollInterval)

	ticker := time.NewTicker(q.opts.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("vtq: consumer stopped", "queue", q.opts.Queue)
			return
		case <-ticker.C:
			q.poll(ctx, handler, log)
		}
	}
}

func (q *Q) poll(ctx context.Context, handler Handler, log *slog.Logger) {
	for {
		job, err := q.Claim(ctx)
		if err != nil {
			log.Warn("vtq: claim failed", "error", err, "queue", q.opts.Queue)
			return
		}
		if job == nil {
			return // nothing visible
		}

		// Discard if max attempts exceeded.
		if q.opts.MaxAttempts > 0 && job.Attempts > q.opts.MaxAttempts {
			log.Warn("vtq: job exceeded max attempts, discarding",
				"id", job.ID, "attempts", job.Attempts, "queue", q.opts.Queue)
			_ = q.Ack(ctx, job.ID)
			continue
		}

		if err := handler(ctx, job); err != nil {
			log.Warn("vtq: handler failed, nacking", "id", job.ID, "error", err, "queue", q.opts.Queue)
			_ = q.Nack(ctx, job.ID)
		} else {
			_ = q.Ack(ctx, job.ID)
		}
	}
}

// BatchClaim atomically claims up to n visible jobs. It returns an empty
// (non-nil) slice when no jobs are available.
func (q *Q) BatchClaim(ctx context.Context, n int) ([]*Job, error) {
	now := time.Now()
	hideUntil := now.Add(q.opts.Visibility).UnixMilli()

	rows, err := q.db.QueryContext(ctx, `
		UPDATE vtq_jobs
		SET visible_at = ?, attempts = attempts + 1
		WHERE id IN (
			SELECT id FROM vtq_jobs
			WHERE queue = ? AND visible_at <= ?
			ORDER BY visible_at ASC
			LIMIT ?
		)
		RETURNING id, queue, payload, visible_at, created_at, attempts`,
		hideUntil, q.opts.Queue, now.UnixMilli(), n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*Job
	for rows.Next() {
		var j Job
		var visAt, creAt int64
		if err := rows.Scan(&j.ID, &j.Queue, &j.Payload, &visAt, &creAt, &j.Attempts); err != nil {
			return nil, err
		}
		j.VisibleAt = time.UnixMilli(visAt)
		j.CreatedAt = time.UnixMilli(creAt)
		jobs = append(jobs, &j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if jobs == nil {
		jobs = []*Job{}
	}
	return jobs, nil
}

// RunBatch polls in batches and processes jobs with bounded concurrency.
// It blocks until ctx is cancelled, draining in-flight handlers before
// returning.
func (q *Q) RunBatch(ctx context.Context, batchSize, maxConcurrency int, handler Handler) {
	log := q.opts.Logger
	log.Info("vtq: batch consumer started",
		"queue", q.opts.Queue,
		"batch_size", batchSize,
		"max_concurrency", maxConcurrency,
		"visibility", q.opts.Visibility,
		"poll", q.opts.PollInterval,
	)

	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup

	ticker := time.NewTicker(q.opts.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("vtq: batch consumer stopping, draining in-flight handlers", "queue", q.opts.Queue)
			wg.Wait()
			log.Info("vtq: batch consumer stopped", "queue", q.opts.Queue)
			return
		case <-ticker.C:
			jobs, err := q.BatchClaim(ctx, batchSize)
			if err != nil {
				if ctx.Err() != nil {
					wg.Wait()
					return
				}
				log.Warn("vtq: batch claim failed", "error", err, "queue", q.opts.Queue)
				continue
			}

			for _, job := range jobs {
				// Discard if max attempts exceeded.
				if q.opts.MaxAttempts > 0 && job.Attempts > q.opts.MaxAttempts {
					log.Warn("vtq: job exceeded max attempts, discarding",
						"id", job.ID, "attempts", job.Attempts, "queue", q.opts.Queue)
					_ = q.Ack(ctx, job.ID)
					continue
				}

				// Acquire semaphore slot (or bail on context cancel).
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					_ = q.Nack(ctx, job.ID)
					wg.Wait()
					return
				}

				wg.Add(1)
				go func(j *Job) {
					defer wg.Done()
					defer func() { <-sem }()

					if err := handler(ctx, j); err != nil {
						log.Warn("vtq: handler failed, nacking", "id", j.ID, "error", err, "queue", q.opts.Queue)
						_ = q.Nack(context.Background(), j.ID)
					} else {
						_ = q.Ack(context.Background(), j.ID)
					}
				}(job)
			}
		}
	}
}

```
╔════════════════════════════════════════════════════════════════════════════╗
║  vtq — Visibility Timeout Queue backed by SQLite (no external broker)    ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  OVERVIEW                                                                ║
║  ────────                                                                ║
║                                                                          ║
║  Pure SQLite queue with visibility timeout. Claimed jobs become           ║
║  invisible for a configurable duration. If not acked, they reappear      ║
║  automatically for another consumer to pick up.                          ║
║                                                                          ║
║  Three distributed patterns by calibration:                              ║
║    - 1 row, N instances   -> leader election                             ║
║    - N rows, N instances  -> work distribution (TDMA)                    ║
║    - visibility < process -> elastic overflow                            ║
║                                                                          ║
║  LIFECYCLE                                                               ║
║  ─────────                                                               ║
║                                                                          ║
║  ┌──────────┐  Publish(id, payload)  ┌────────────────────────────────┐  ║
║  │ Producer │ ─────────────────────> │ vtq_jobs table                │  ║
║  │          │  visible_at = now      │                                │  ║
║  └──────────┘                        │ id | queue | payload |        │  ║
║                                      │    visible_at | created_at |  │  ║
║                                      │    attempts                   │  ║
║  ┌──────────┐  Claim()               │                                │  ║
║  │ Consumer │ <───────────────────── │ UPDATE SET visible_at=now+vis │  ║
║  │          │  returns *Job          │ WHERE visible_at <= now       │  ║
║  │          │                        │ ORDER BY visible_at LIMIT 1   │  ║
║  │          │                        │ RETURNING ...                  │  ║
║  │          │                        └────────────────────────────────┘  ║
║  │          │                                                            ║
║  │          │── success ──> Ack(id) ──> DELETE FROM vtq_jobs             ║
║  │          │                                                            ║
║  │          │── failure ──> Nack(id) ──> SET visible_at = 0              ║
║  │          │               (immediate re-visibility)                    ║
║  │          │                                                            ║
║  │          │── need more time ──> Extend(job, extra)                    ║
║  │          │                      SET visible_at = now + extra          ║
║  │          │                      (validates caller still holds claim)  ║
║  │          │                      returns ErrNotHolder if expired       ║
║  └──────────┘                                                            ║
║                                                                          ║
║  VISIBILITY TIMEOUT                                                      ║
║  ──────────────────                                                      ║
║                                                                          ║
║  t=0     Publish: visible_at = now (immediately visible)                 ║
║  t=1     Claim:   visible_at = now + 30s (invisible for 30s)             ║
║  t=31    Timeout: visible_at <= now again (job reappears)                ║
║  t=31    Claim by instance B: visible_at = now + 30s                     ║
║                                                                          ║
║     ┌──── visible ────┐ ┌──── invisible ─────────────┐ ┌── visible ──   ║
║     │                 │ │                             │ │                ║
║  ───┼─────────────────┼─┼─────────────────────────────┼─┼────────────>  ║
║     0        Claim    1 │                           31 │    time        ║
║              by A       │   A processing...           │   B claims     ║
║                         │   (if A acks, job deleted)  │                 ║
║                         │   (if A crashes, reappears) │                 ║
║                                                                          ║
║  CONSUMER LOOPS                                                          ║
║  ──────────────                                                          ║
║                                                                          ║
║  Sequential (Run):                                                       ║
║  ┌────────────────────────────────────────────────────┐                  ║
║  │ ticker(PollInterval)                               │                  ║
║  │   └──> poll() loop:                                │                  ║
║  │          Claim() -> if nil, return                  │                  ║
║  │          if attempts > MaxAttempts: Ack (discard)   │                  ║
║  │          handler(ctx, job)                          │                  ║
║  │            success -> Ack(id)                       │                  ║
║  │            error   -> Nack(id)                      │                  ║
║  │          loop again (drain visible jobs)            │                  ║
║  └────────────────────────────────────────────────────┘                  ║
║                                                                          ║
║  Concurrent (RunBatch):                                                  ║
║  ┌────────────────────────────────────────────────────┐                  ║
║  │ ticker(PollInterval)                               │                  ║
║  │   └──> BatchClaim(n) -> []*Job                     │                  ║
║  │        for each job:                                │                  ║
║  │          acquire semaphore (maxConcurrency)         │                  ║
║  │          go func():                                 │                  ║
║  │            handler(ctx, job)                         │                  ║
║  │            success -> Ack    |    error -> Nack      │                  ║
║  │            release semaphore                         │                  ║
║  │        on ctx.Done: Nack remaining, wg.Wait()       │                  ║
║  └────────────────────────────────────────────────────┘                  ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  DATABASE TABLE (SQLite, created by EnsureTable)                         ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  vtq_jobs                                                                ║
║  ┌────────────┬─────────┬─────────────────────────────────────────┐      ║
║  │ id         │ TEXT    │ PK, caller-provided job ID               │      ║
║  │ queue      │ TEXT    │ NOT NULL DEFAULT '', logical queue name  │      ║
║  │ payload    │ BLOB    │ opaque job data                          │      ║
║  │ visible_at │ INTEGER │ NOT NULL DEFAULT 0, ms since epoch       │      ║
║  │ created_at │ INTEGER │ NOT NULL, ms since epoch                 │      ║
║  │ attempts   │ INTEGER │ NOT NULL DEFAULT 0, claim count          │      ║
║  └────────────┴─────────┴─────────────────────────────────────────┘      ║
║                                                                          ║
║  INDEX: idx_vtq_visible ON vtq_jobs(queue, visible_at)                   ║
║                                                                          ║
║  Claim SQL (atomic):                                                     ║
║    UPDATE vtq_jobs SET visible_at=?, attempts=attempts+1                 ║
║    WHERE id = (SELECT id FROM vtq_jobs                                   ║
║                WHERE queue=? AND visible_at<=? ORDER BY visible_at       ║
║                LIMIT 1)                                                  ║
║    RETURNING id, queue, payload, visible_at, created_at, attempts        ║
║                                                                          ║
║  BatchClaim SQL (atomic, up to N):                                       ║
║    UPDATE vtq_jobs SET visible_at=?, attempts=attempts+1                 ║
║    WHERE id IN (SELECT id FROM vtq_jobs                                  ║
║                 WHERE queue=? AND visible_at<=? ORDER BY visible_at      ║
║                 LIMIT ?)                                                 ║
║    RETURNING ...                                                         ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  KEY TYPES                                                               ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  Q              Queue handle {db *sql.DB, opts Options}                  ║
║                                                                          ║
║  Job            {ID string, Queue string, Payload []byte,                ║
║                  VisibleAt time.Time, CreatedAt time.Time,               ║
║                  Attempts int}                                           ║
║                                                                          ║
║  Options        {Queue string, Visibility time.Duration (default 30s),   ║
║                  PollInterval time.Duration (default 1s),                ║
║                  MaxAttempts int (0=unlimited),                           ║
║                  Logger *slog.Logger}                                    ║
║                                                                          ║
║  Handler        func(ctx context.Context, job *Job) error                ║
║                 return nil -> ack, return error -> nack                   ║
║                                                                          ║
║  ErrNotHolder   sentinel error for Extend when claim expired             ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  KEY FUNCTIONS                                                           ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  New(db, Options) *Q                                                     ║
║  Q.EnsureTable(ctx) error           CREATE TABLE IF NOT EXISTS           ║
║  Q.Publish(ctx, id, payload) error  Insert immediately visible job       ║
║  Q.Claim(ctx) (*Job, error)         Atomic claim oldest visible job      ║
║  Q.BatchClaim(ctx, n) ([]*Job, err) Atomic claim up to n visible jobs    ║
║  Q.Ack(ctx, id) error               Delete processed job                ║
║  Q.Nack(ctx, id) error              Make job immediately visible again   ║
║  Q.Extend(ctx, job, extra) error    Push visibility forward (heartbeat)  ║
║  Q.Purge(ctx) error                 Delete all jobs in queue             ║
║  Q.Len(ctx) (int, error)            Count all jobs (visible+invisible)   ║
║  Q.Run(ctx, Handler)                Sequential consumer loop (blocking)  ║
║  Q.RunBatch(ctx, batchSize,         Concurrent consumer loop (blocking)  ║
║      maxConcurrency, Handler)       with semaphore-bounded goroutines    ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES                                                            ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  Standard library only: database/sql, context, log/slog, sync, time      ║
║  No hazyhaar/pkg dependencies.                                           ║
║                                                                          ║
╠════════════════════════════════════════════════════════════════════════════╣
║  EXTEND SAFETY (heartbeat pattern)                                       ║
╠════════════════════════════════════════════════════════════════════════════╣
║                                                                          ║
║  Extend validates the caller still holds the claim:                      ║
║    WHERE id=? AND queue=? AND visible_at=? AND visible_at > now          ║
║                                                                          ║
║  - visible_at = job.VisibleAt ensures no one re-claimed since Claim      ║
║  - visible_at > now ensures the visibility window hasn't expired         ║
║  - RowsAffected == 0 -> ErrNotHolder (claim lost)                        ║
║  - On success: job.VisibleAt updated in place for next Extend call       ║
║                                                                          ║
║  Typical heartbeat loop:                                                 ║
║    for processing {                                                      ║
║      if err := q.Extend(ctx, job, 30s); err == ErrNotHolder {            ║
║        // lost claim, abort processing                                   ║
║        return                                                            ║
║      }                                                                   ║
║    }                                                                     ║
║                                                                          ║
╚════════════════════════════════════════════════════════════════════════════╝
```

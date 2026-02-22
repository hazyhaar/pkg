# vtq — Visibility Timeout Queue (SQLite)

Pure-Go, single-table job queue with SQS-style visibility timeout.
One primitive, three patterns — just calibration.

```
         Publish ──► ┌──────────────┐
                     │  vtq_jobs    │
                     │              │  visible_at <= now()
         Claim  ◄─── │  id          │ ◄── consumer polls
                     │  payload     │
                     │  visible_at ─┤── invisible while held
                     │  attempts    │
                     └──────┬───────┘
                            │
              ┌─────────────┼─────────────┐
              ▼             ▼             ▼
           Ack           Nack         Timeout
         (delete)    (reappear now)  (reappear later)
```

## Patterns

| Configuration | Result |
|---|---|
| 1 row, N instances | **Leader election** — one holder, automatic failover |
| N rows, N instances | **Work distribution** (TDMA) |
| visibility < processing time under load | **Elastic overflow** — slow instance loses rows, others pick up |

## Quick start

```go
db, _ := sql.Open("sqlite", "app.db")

q := vtq.New(db, vtq.Options{
    Queue:      "emails",
    Visibility: 30 * time.Second,
})
q.EnsureTable(ctx)

// Producer
q.Publish(ctx, "msg-001", []byte(`{"to":"a@b.com"}`))

// Consumer (blocks until ctx cancelled)
q.Run(ctx, func(ctx context.Context, job *vtq.Job) error {
    return sendEmail(job.Payload)
    // return nil  → Ack (deleted)
    // return err  → Nack (reappears immediately)
})
```

## Leader election

```go
q := vtq.New(db, vtq.Options{
    Queue:      "leader",
    Visibility: 10 * time.Second,
})
q.EnsureTable(ctx)

// Seed the token once.
q.Publish(ctx, "leader-token", nil)

// Each instance tries to claim.
for {
    job, _ := q.Claim(ctx)
    if job != nil {
        // I am the leader — renew before timeout.
        go keepAlive(ctx, q, job.ID, 5*time.Second)
        doLeaderWork(ctx)
        break
    }
    time.Sleep(time.Second)
}

func keepAlive(ctx context.Context, q *vtq.Q, id string, interval time.Duration) {
    for {
        select {
        case <-ctx.Done():
            return
        case <-time.After(interval):
            q.Extend(ctx, id, 10*time.Second)
        }
    }
}
```

## API

| Function | Description |
|---|---|
| `New(db, Options) *Q` | Create a queue handle |
| `EnsureTable(ctx)` | Create table + index if missing |
| `Publish(ctx, id, payload)` | Insert a visible job |
| `Claim(ctx) (*Job, error)` | Atomically pick the oldest visible job |
| `Ack(ctx, id)` | Delete a processed job |
| `Nack(ctx, id)` | Make a job visible immediately |
| `Extend(ctx, id, duration)` | Push visibility timeout forward |
| `Purge(ctx)` | Delete all jobs in the queue |
| `Len(ctx) (int, error)` | Count total jobs (visible + invisible) |
| `Run(ctx, Handler)` | Poll loop — claim, handle, ack/nack |

## Options

| Field | Default | Description |
|---|---|---|
| `Queue` | `""` | Logical queue name (multiple queues share one table) |
| `Visibility` | `30s` | How long a claimed job stays invisible |
| `PollInterval` | `1s` | Delay between claim attempts in Run |
| `MaxAttempts` | `0` | Max redeliveries before discard (0 = unlimited) |
| `Logger` | `slog.Default()` | Structured logger |

## Schema

```sql
CREATE TABLE IF NOT EXISTS vtq_jobs (
    id          TEXT PRIMARY KEY,
    queue       TEXT NOT NULL DEFAULT '',
    payload     BLOB,
    visible_at  INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    attempts    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_vtq_visible ON vtq_jobs (queue, visible_at);
```

## Combining with dbsync for HA

```
┌──────────┐  vtq: claim leader-token  ┌──────────┐
│ Instance │ ◄────────────────────────► │  SQLite   │
│ A (leader)                            │ vtq_jobs  │
│          │ ──── dbsync snapshots ───► │          │
└──────────┘                            └──────────┘
                                              ▲
┌──────────┐  vtq: claim → nil (blocked)      │
│ Instance │ ◄────────────────────────────────┘
│ B (standby)   reads via dbsync replica
└──────────┘
     │
     └── If A crashes → visibility expires → B claims → becomes leader
```

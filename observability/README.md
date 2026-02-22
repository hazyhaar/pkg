# observability — SQLite-native monitoring

`observability` replaces Prometheus, Grafana, and ELK with SQLite tables. All
writers are async and non-blocking to avoid impacting application latency.

## Components

| Component | Table | Pattern |
|-----------|-------|---------|
| `AuditLogger` | `audit_log` | Buffered channel + batch flush |
| `MetricsManager` | `metrics_timeseries` | Buffered batch (100 entries / 5 s) |
| `EventLogger` | `business_event_logs` | Synchronous single-insert |
| `HeartbeatWriter` | `worker_heartbeats` | Periodic ticker with runtime metrics |

## Quick start

```go
observability.Init(db)

audit := observability.NewAuditLogger(db, 1000,
    observability.WithAuditIDGenerator(idgen.Prefixed("aud_", idgen.Default)),
)
defer audit.Close()

metrics := observability.NewMetricsManager(db, 100, 5*time.Second)
defer metrics.Close()

hb := observability.NewHeartbeatWriter(db, "worker-1", 15*time.Second)
hb.Start(ctx)
defer hb.Stop()
```

## Heartbeat with runtime metrics

Each heartbeat row captures Go runtime stats alongside liveness:

```go
// Automatic: goroutine count, memory alloc/sys, GC count.
status, _ := observability.LatestHeartbeat(ctx, db, "worker-1", 30*time.Second)
// status.Alive, status.StaleSince
```

## Retention cleanup

```go
audit.Cleanup(ctx, 90)   // delete entries older than 90 days
metrics.Cleanup(ctx, 30)  // 30 days
observability.CleanupHeartbeats(ctx, db, 7)
```

## Schema

8 tables: `worker_heartbeats`, `metrics_timeseries`, `metrics_metadata`,
`audit_log`, `business_event_logs`, `system_alerts`, `http_request_logs`,
`_observability_metadata`.

## Exported API

| Symbol | Description |
|--------|-------------|
| `Init(db)` | Create all tables |
| `AuditLogger` | Async audit trail (buffered channel) |
| `MetricsManager` | Buffered time-series writer |
| `EventLogger` | Business event logger |
| `HeartbeatWriter` | Periodic liveness + runtime metrics |
| `LatestHeartbeat(ctx, db, name, threshold)` | Query worker health |

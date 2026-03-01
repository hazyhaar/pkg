╔═══════════════════════════════════════════════════════════════════════════════╗
║  observability — SQLite-native monitoring: audit, heartbeat, metrics, events ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  Replaces: Prometheus (metrics), Loki (logs), Consul (health), ES (audit)   ║
║  All writes are async/non-blocking. Buffer overflow drops, never backpress. ║
║  DB must be SEPARATE from app DB to avoid write contention.                 ║
║                                                                              ║
║  ARCHITECTURE                                                                ║
║  ~~~~~~~~~~~~                                                                ║
║                                                                              ║
║                     ┌──────────────────────────────────────────────┐          ║
║                     │         Observability SQLite DB              │          ║
║                     │              Init(db)                        │          ║
║                     └──────┬──────┬──────┬──────┬──────┬──────────┘          ║
║                            │      │      │      │      │                     ║
║              ┌─────────────┘  ┌───┘  ┌───┘  ┌───┘  ┌───┘                    ║
║              v                v      v      v      v                         ║
║  ┌────────────────┐ ┌──────────┐ ┌───────┐ ┌────────┐ ┌──────────────┐      ║
║  │ AuditLogger    │ │Heartbeat │ │Metrics│ │ Event  │ │ Cleanup()    │      ║
║  │                │ │ Writer   │ │Manager│ │ Logger │ │              │      ║
║  │ async buffer   │ │          │ │       │ │        │ │ retention    │      ║
║  │ (chan, batch)   │ │ periodic │ │ batch │ │ sync   │ │ per-table    │      ║
║  └───────┬────────┘ │ ticker   │ │ flush │ │ insert │ │ + VACUUM     │      ║
║          │          └────┬─────┘ └───┬───┘ └───┬────┘ └──────┬───────┘      ║
║          v               v           v         v             v               ║
║  ┌──────────┐   ┌──────────────┐ ┌────────┐ ┌──────────┐ ┌─────────┐       ║
║  │audit_log │   │worker_       │ │metrics_│ │business_ │ │DELETE   │       ║
║  │          │   │heartbeats    │ │time-   │ │event_    │ │WHERE <  │       ║
║  │          │   │              │ │series  │ │logs      │ │cutoff   │       ║
║  └──────────┘   └──────────────┘ └────────┘ └──────────┘ └─────────┘       ║
║                                                                              ║
║  COMPONENT DETAIL                                                            ║
║  ~~~~~~~~~~~~~~~~                                                            ║
║                                                                              ║
║  AuditLogger:                                                                ║
║  ┌──────────┐   chan(bufSize)   ┌────────────┐   batch TX   ┌──────────┐    ║
║  │ LogAsync │ ──────────────── │ flushLoop  │ ──────────── │audit_log │    ║
║  │ (entry)  │   full? sync     │ ticker 5s  │  100 entries │          │    ║
║  └──────────┘   fallback       │ or 100 cap │  per commit  └──────────┘    ║
║  ┌──────────┐                  └────────────┘                               ║
║  │ Log()    │ ── sync insert ──────────────────────────────>│audit_log │    ║
║  └──────────┘                                               └──────────┘    ║
║                                                                              ║
║  HeartbeatWriter:                                                            ║
║  ┌──────────────┐  ticker(interval)   ┌──────────────────────┐              ║
║  │ Start(ctx)   │ ──────────────────> │ worker_heartbeats    │              ║
║  │ WriteHbeat() │  + immediate first  │ + runtime.MemStats   │              ║
║  └──────────────┘                     └──────────────────────┘              ║
║                                                                              ║
║  MetricsManager:                                                             ║
║  ┌──────────────┐   in-memory buf    ┌────────────┐  batch TX  ┌─────────┐  ║
║  │ Record(m)    │ ─────────────────> │ flushLoop  │ ────────> │metrics_ │  ║
║  │ RecordSimple │   flush at cap     │ ticker(int)│           │timeseries│  ║
║  └──────────────┘   or on ticker     └────────────┘           └─────────┘  ║
║                                                                              ║
║  EventLogger:                                                                ║
║  ┌──────────────┐   sync insert    ┌──────────────────────┐                 ║
║  │ LogEvent()   │ ───────────────> │ business_event_logs  │                 ║
║  │ LogHeartbeat │ ───────────────> │ worker_heartbeats    │                 ║
║  └──────────────┘  (non-fatal)     └──────────────────────┘                 ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  DATABASE TABLES (7 tables + 1 metadata registry)                            ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  worker_heartbeats                                                           ║
║  ├── heartbeat_id TEXT PK (auto: 'hb_' || hex(randomblob(16)))              ║
║  ├── worker_name TEXT NOT NULL                                               ║
║  ├── hostname TEXT NOT NULL                                                  ║
║  ├── worker_pid INTEGER NOT NULL                                             ║
║  ├── timestamp INTEGER NOT NULL       (epoch seconds)                       ║
║  ├── goroutines_count INTEGER                                                ║
║  ├── memory_alloc_mb REAL                                                    ║
║  ├── memory_sys_mb REAL                                                      ║
║  ├── gc_count INTEGER                                                        ║
║  └── created_at INTEGER                                                      ║
║  IDX: (worker_name, timestamp DESC), (timestamp DESC)                       ║
║                                                                              ║
║  metrics_timeseries                                                          ║
║  ├── metric_id TEXT PK (auto: 'met_' || hex(randomblob(16)))                ║
║  ├── metric_name TEXT NOT NULL                                               ║
║  ├── timestamp INTEGER NOT NULL                                              ║
║  ├── value REAL NOT NULL                                                     ║
║  ├── labels TEXT                       (JSON: {"key":"val"})                ║
║  ├── unit TEXT                                                               ║
║  └── created_at INTEGER                                                      ║
║  IDX: (metric_name, timestamp DESC), (timestamp DESC)                       ║
║                                                                              ║
║  metrics_metadata                                                            ║
║  ├── metric_name TEXT PK                                                     ║
║  ├── metric_type TEXT NOT NULL                                               ║
║  ├── description TEXT                                                        ║
║  ├── first_seen INTEGER NOT NULL                                             ║
║  └── last_seen INTEGER NOT NULL                                              ║
║                                                                              ║
║  audit_log                                                                   ║
║  ├── entry_id TEXT PK                                                        ║
║  ├── timestamp INTEGER NOT NULL                                              ║
║  ├── component_name TEXT NOT NULL                                            ║
║  ├── operation_type TEXT NOT NULL                                            ║
║  ├── user_id TEXT, session_id TEXT, request_id TEXT                          ║
║  ├── parameters TEXT DEFAULT '{}'      (JSON)                               ║
║  ├── result TEXT                       (JSON)                               ║
║  ├── error_code TEXT, error_message TEXT                                     ║
║  ├── duration_ms INTEGER                                                     ║
║  ├── status TEXT NOT NULL              ('success'|'error'|'timeout'|'canc') ║
║  ├── metadata TEXT                     (free-form JSON)                     ║
║  └── created_at INTEGER                                                      ║
║  IDX: (timestamp DESC), (component_name, operation_type), (status)          ║
║                                                                              ║
║  business_event_logs                                                         ║
║  ├── event_id TEXT PK                                                        ║
║  ├── event_type TEXT NOT NULL                                                ║
║  ├── service_name TEXT NOT NULL                                              ║
║  ├── entity_type TEXT, entity_id TEXT                                        ║
║  ├── user_id TEXT                                                            ║
║  ├── action TEXT NOT NULL                                                    ║
║  ├── details TEXT                      (JSON)                               ║
║  ├── success INTEGER DEFAULT 1                                               ║
║  └── created_at INTEGER                                                      ║
║  IDX: (event_type, created_at DESC), (service_name, created_at DESC)        ║
║                                                                              ║
║  system_alerts                                                               ║
║  ├── alert_id TEXT PK (auto: 'alert_' || hex(randomblob(16)))               ║
║  ├── alert_type TEXT NOT NULL                                                ║
║  ├── severity TEXT NOT NULL                                                  ║
║  ├── component_id TEXT                                                       ║
║  ├── detected_at INTEGER NOT NULL                                            ║
║  ├── resolved_at INTEGER               (NULL = unresolved)                  ║
║  ├── title TEXT NOT NULL                                                     ║
║  ├── description TEXT, context_data TEXT                                     ║
║  └── created_at INTEGER                                                      ║
║  IDX: (severity, detected_at DESC), partial (resolved_at) WHERE NULL        ║
║                                                                              ║
║  http_request_logs                                                           ║
║  ├── log_id TEXT PK (auto: 'hrl_' || hex(randomblob(16)))                   ║
║  ├── method TEXT, path TEXT, status_code INTEGER                             ║
║  ├── duration_ms INTEGER, user_id TEXT, ip_address TEXT, user_agent TEXT     ║
║  └── created_at INTEGER                                                      ║
║  IDX: (created_at DESC)                                                      ║
║                                                                              ║
║  _observability_metadata                  (registry of table descriptions)   ║
║  ├── table_name TEXT PK                                                      ║
║  ├── created_at INTEGER                                                      ║
║  └── description TEXT                                                        ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  EXPORTED TYPES                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  AuditLogger       struct  Async audit with buffered channel + batch TX      ║
║  AuditEntry        struct  Single audit record (14 fields)                   ║
║  AuditFilter       struct  Query filter: time range, component, status, pag  ║
║  AuditOption       func    Functional opts for AuditLogger                   ║
║  MetricsManager    struct  Buffered timeseries writer + query                ║
║  Metric            struct  Single datapoint: name, timestamp, value, labels  ║
║  HeartbeatWriter   struct  Periodic liveness probe with runtime stats        ║
║  HeartbeatStatus   struct  Latest heartbeat + alive/stale computed flag      ║
║  RuntimeMetrics    struct  Goroutines, MemAlloc, MemSys, GCCount            ║
║  EventLogger       struct  Sync business event + heartbeat writer            ║
║  BusinessEvent     struct  Domain event: type, service, entity, action       ║
║  EventLoggerOption func    Functional opts for EventLogger                   ║
║  RetentionConfig   struct  Per-table retention days + vacuum flag             ║
║                                                                              ║
║  Constants (standard metric names):                                          ║
║    MetricCPUUsagePercent, MetricMemoryUsedBytes, MetricMemoryAllocMB,       ║
║    MetricGoroutinesCount, MetricGCCount, MetricWorkflowDurationMs,          ║
║    MetricTaskProcessedCount                                                  ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  KEY FUNCTIONS                                                               ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  Init(db) error                           -- apply full Schema DDL           ║
║                                                                              ║
║  NewAuditLogger(db, bufSize, ...opt) *AuditLogger                            ║
║  AuditLogger.Log(ctx, *AuditEntry) error          -- sync insert            ║
║  AuditLogger.LogAsync(*AuditEntry)                 -- buffered, fallback     ║
║  AuditLogger.NewAuditEntry(comp, op, params, res, err, dur) *AuditEntry     ║
║  AuditLogger.Query(ctx, *AuditFilter) ([]*AuditEntry, error)                ║
║  AuditLogger.Cleanup(ctx, retentionDays) (int64, error)                     ║
║  AuditLogger.Close() error                         -- drain + stop           ║
║                                                                              ║
║  NewMetricsManager(db, bufSize, flushInterval) *MetricsManager               ║
║  MetricsManager.Record(*Metric)                    -- async buffered         ║
║  MetricsManager.RecordSimple(name, value, unit)    -- convenience            ║
║  MetricsManager.Query(name, start, end, limit) ([]*Metric, error)           ║
║  MetricsManager.Cleanup(ctx, retentionDays) (int64, error)                  ║
║  MetricsManager.Close() error                                                ║
║                                                                              ║
║  NewHeartbeatWriter(db, workerName, interval) *HeartbeatWriter               ║
║  HeartbeatWriter.Start(ctx)                        -- launches goroutine     ║
║  HeartbeatWriter.WriteHeartbeat() error            -- single beat            ║
║  HeartbeatWriter.Stop()                            -- signal + wait          ║
║  CollectRuntimeMetrics() RuntimeMetrics            -- ~10us overhead         ║
║  LatestHeartbeat(ctx, db, worker, threshold) (*HeartbeatStatus, error)       ║
║  CleanupHeartbeats(ctx, db, days) (int64, error)                             ║
║                                                                              ║
║  NewEventLogger(db, ...opt) *EventLogger                                     ║
║  EventLogger.LogEvent(ctx, BusinessEvent)          -- sync, non-fatal        ║
║  EventLogger.LogHeartbeat(ctx, worker, pid, host)  -- lightweight beat       ║
║                                                                              ║
║  Cleanup(ctx, db, RetentionConfig) error           -- multi-table cleanup    ║
║    targets: http_request_logs, business_event_logs, worker_heartbeats       ║
║    optional VACUUM after cleanup                                             ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  DEPENDENCIES                                                                ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  github.com/hazyhaar/pkg/idgen   -- Prefixed UUID generation (evt_, audit_) ║
║  Standard library only otherwise: database/sql, log/slog, runtime, os       ║
║                                                                              ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  FLUSH / LIFECYCLE                                                           ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║                                                                              ║
║  AuditLogger flush: ticker 5s OR batch >= 100 entries. Uses prepared stmt   ║
║  in a single TX. On Close(): drain channel, flush remaining, exit.          ║
║                                                                              ║
║  MetricsManager flush: ticker(flushInterval) OR buffer >= bufferSize.       ║
║  On Close(): flush locked buffer, exit.                                      ║
║                                                                              ║
║  HeartbeatWriter: immediate first beat, then ticker(interval). Stops on     ║
║  ctx.Done() or Stop(). Errors logged via slog, never propagated.            ║
║                                                                              ║
║  All timestamps stored as epoch seconds (int64), not RFC3339.               ║
║                                                                              ║
╚═══════════════════════════════════════════════════════════════════════════════╝

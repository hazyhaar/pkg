package observability

import "database/sql"

// Schema contains the complete DDL for the observability tables.
// Call Init(db) to apply it, or use this constant to embed in your own
// schema management.
const Schema = `
-- Worker Heartbeats
CREATE TABLE IF NOT EXISTS worker_heartbeats (
    heartbeat_id TEXT PRIMARY KEY DEFAULT ('hb_' || hex(randomblob(16))),
    worker_name TEXT NOT NULL,
    hostname TEXT NOT NULL,
    worker_pid INTEGER NOT NULL,
    timestamp INTEGER NOT NULL,
    goroutines_count INTEGER,
    memory_alloc_mb REAL,
    memory_sys_mb REAL,
    gc_count INTEGER,
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_heartbeats_worker_time
    ON worker_heartbeats(worker_name, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_heartbeats_timestamp
    ON worker_heartbeats(timestamp DESC);

-- Metrics Timeseries
CREATE TABLE IF NOT EXISTS metrics_timeseries (
    metric_id TEXT PRIMARY KEY DEFAULT ('met_' || hex(randomblob(16))),
    metric_name TEXT NOT NULL,
    timestamp INTEGER NOT NULL,
    value REAL NOT NULL,
    labels TEXT,
    unit TEXT,
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_metrics_name_time
    ON metrics_timeseries(metric_name, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_metrics_timestamp
    ON metrics_timeseries(timestamp DESC);

CREATE TABLE IF NOT EXISTS metrics_metadata (
    metric_name TEXT PRIMARY KEY,
    metric_type TEXT NOT NULL,
    description TEXT,
    first_seen INTEGER NOT NULL,
    last_seen INTEGER NOT NULL
);

-- Audit Log
CREATE TABLE IF NOT EXISTS audit_log (
    entry_id TEXT PRIMARY KEY,
    timestamp INTEGER NOT NULL,
    component_name TEXT NOT NULL,
    operation_type TEXT NOT NULL,
    user_id TEXT,
    session_id TEXT,
    request_id TEXT,
    parameters TEXT NOT NULL DEFAULT '{}',
    result TEXT,
    error_code TEXT,
    error_message TEXT,
    duration_ms INTEGER,
    status TEXT NOT NULL,
    metadata TEXT,
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_log(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_audit_component ON audit_log(component_name, operation_type);
CREATE INDEX IF NOT EXISTS idx_audit_status ON audit_log(status);

-- Business Event Logs
CREATE TABLE IF NOT EXISTS business_event_logs (
    event_id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    service_name TEXT NOT NULL,
    entity_type TEXT,
    entity_id TEXT,
    user_id TEXT,
    action TEXT NOT NULL,
    details TEXT,
    success INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_event_logs_type ON business_event_logs(event_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_event_logs_service ON business_event_logs(service_name, created_at DESC);

-- System Alerts
CREATE TABLE IF NOT EXISTS system_alerts (
    alert_id TEXT PRIMARY KEY DEFAULT ('alert_' || hex(randomblob(16))),
    alert_type TEXT NOT NULL,
    severity TEXT NOT NULL,
    component_id TEXT,
    detected_at INTEGER NOT NULL,
    resolved_at INTEGER,
    title TEXT NOT NULL,
    description TEXT,
    context_data TEXT,
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_alerts_severity_time
    ON system_alerts(severity, detected_at DESC);
CREATE INDEX IF NOT EXISTS idx_alerts_unresolved
    ON system_alerts(resolved_at) WHERE resolved_at IS NULL;

-- HTTP Request Logs (for retention cleanup)
CREATE TABLE IF NOT EXISTS http_request_logs (
    log_id TEXT PRIMARY KEY DEFAULT ('hrl_' || hex(randomblob(16))),
    method TEXT NOT NULL,
    path TEXT NOT NULL,
    status_code INTEGER,
    duration_ms INTEGER,
    user_id TEXT,
    ip_address TEXT,
    user_agent TEXT,
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_http_logs_time ON http_request_logs(created_at DESC);

-- Metadata registry
CREATE TABLE IF NOT EXISTS _observability_metadata (
    table_name TEXT PRIMARY KEY,
    created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    description TEXT
);
INSERT OR IGNORE INTO _observability_metadata (table_name, description) VALUES
    ('worker_heartbeats', 'Worker liveness heartbeats with runtime metrics'),
    ('metrics_timeseries', 'Timeseries metric datapoints'),
    ('metrics_metadata', 'Metric type definitions'),
    ('audit_log', 'Operation-level audit trail'),
    ('business_event_logs', 'Domain-level business events'),
    ('system_alerts', 'Automated anomaly alerts'),
    ('http_request_logs', 'HTTP request logs');
`

// Init applies the observability schema to the given database.
func Init(db *sql.DB) error {
	_, err := db.Exec(Schema)
	return err
}

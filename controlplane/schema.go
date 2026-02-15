// Package controlplane implements the SQLite control plane — the core
// innovation of hpx. All runtime configuration lives in 12 SQLite tables
// that are hot-reloaded via PRAGMA data_version polling.
//
// An LLM (or any SQL client) can administer the entire system through
// simple INSERT/UPDATE/DELETE statements on these tables.
package controlplane

// Schema contains the complete DDL for the hpx control plane.
// Apply it once via Init(), or include it in your own migration chain.
const Schema = `
-- ============================================================
-- hpx_config: Key-value configuration store
-- ============================================================
CREATE TABLE IF NOT EXISTS hpx_config (
    key         TEXT PRIMARY KEY,
    value       TEXT NOT NULL,
    description TEXT,
    updated_at  INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

CREATE TRIGGER IF NOT EXISTS trg_hpx_config_updated
AFTER UPDATE ON hpx_config FOR EACH ROW
BEGIN
    UPDATE hpx_config SET updated_at = strftime('%s', 'now') WHERE key = NEW.key;
END;

-- ============================================================
-- hpx_routes: Service routing table (Job as Library core)
-- ============================================================
CREATE TABLE IF NOT EXISTS hpx_routes (
    service_name TEXT PRIMARY KEY,
    strategy     TEXT NOT NULL CHECK(strategy IN ('local', 'http', 'quic', 'grpc', 'mcp', 'noop')),
    endpoint     TEXT,
    config       TEXT DEFAULT '{}',
    priority     INTEGER NOT NULL DEFAULT 0,
    enabled      INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
    updated_at   INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_hpx_routes_strategy ON hpx_routes(strategy);
CREATE INDEX IF NOT EXISTS idx_hpx_routes_enabled ON hpx_routes(enabled) WHERE enabled = 1;

CREATE TRIGGER IF NOT EXISTS trg_hpx_routes_updated
AFTER UPDATE ON hpx_routes FOR EACH ROW
BEGIN
    UPDATE hpx_routes SET updated_at = strftime('%s', 'now') WHERE service_name = NEW.service_name;
END;

-- ============================================================
-- hpx_middleware: Ordered middleware chain per route
-- ============================================================
CREATE TABLE IF NOT EXISTS hpx_middleware (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    service_name TEXT NOT NULL REFERENCES hpx_routes(service_name) ON DELETE CASCADE,
    middleware   TEXT NOT NULL,
    position     INTEGER NOT NULL DEFAULT 0,
    config       TEXT DEFAULT '{}',
    enabled      INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1)),
    UNIQUE(service_name, middleware)
);

CREATE INDEX IF NOT EXISTS idx_hpx_middleware_chain ON hpx_middleware(service_name, position);

-- ============================================================
-- hpx_services: Service discovery registry
-- ============================================================
CREATE TABLE IF NOT EXISTS hpx_services (
    service_name TEXT PRIMARY KEY,
    version      TEXT,
    host         TEXT,
    port         INTEGER,
    protocol     TEXT DEFAULT 'http',
    metadata     TEXT DEFAULT '{}',
    health_url   TEXT,
    status       TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active', 'draining', 'inactive')),
    last_seen    INTEGER,
    registered_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_hpx_services_status ON hpx_services(status);

-- ============================================================
-- hpx_ratelimits: Rate limiting rules
-- ============================================================
CREATE TABLE IF NOT EXISTS hpx_ratelimits (
    rule_id      TEXT PRIMARY KEY,
    service_name TEXT,
    scope        TEXT NOT NULL DEFAULT 'global' CHECK(scope IN ('global', 'service', 'user', 'ip')),
    max_requests INTEGER NOT NULL,
    window_ms    INTEGER NOT NULL,
    burst        INTEGER DEFAULT 0,
    enabled      INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0, 1))
);

-- ============================================================
-- hpx_ratelimit_counters: Rate limit state (ephemeral)
-- ============================================================
CREATE TABLE IF NOT EXISTS hpx_ratelimit_counters (
    counter_key  TEXT PRIMARY KEY,
    rule_id      TEXT NOT NULL REFERENCES hpx_ratelimits(rule_id) ON DELETE CASCADE,
    tokens       REAL NOT NULL,
    last_refill  INTEGER NOT NULL,
    request_count INTEGER NOT NULL DEFAULT 0
);

-- ============================================================
-- hpx_breakers: Circuit breaker state
-- ============================================================
CREATE TABLE IF NOT EXISTS hpx_breakers (
    service_name  TEXT PRIMARY KEY,
    state         TEXT NOT NULL DEFAULT 'closed' CHECK(state IN ('closed', 'open', 'half_open')),
    failure_count INTEGER NOT NULL DEFAULT 0,
    success_count INTEGER NOT NULL DEFAULT 0,
    threshold     INTEGER NOT NULL DEFAULT 5,
    reset_timeout_ms INTEGER NOT NULL DEFAULT 30000,
    last_failure  INTEGER,
    last_state_change INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

-- ============================================================
-- hpx_certs: TLS certificate store
-- ============================================================
CREATE TABLE IF NOT EXISTS hpx_certs (
    cert_id     TEXT PRIMARY KEY,
    domain      TEXT NOT NULL,
    cert_pem    TEXT NOT NULL,
    key_pem     TEXT NOT NULL,
    ca_pem      TEXT,
    not_before  INTEGER,
    not_after   INTEGER,
    is_default  INTEGER NOT NULL DEFAULT 0 CHECK(is_default IN (0, 1)),
    created_at  INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_hpx_certs_domain ON hpx_certs(domain);

-- ============================================================
-- hpx_topics: Message bus topic registry
-- ============================================================
CREATE TABLE IF NOT EXISTS hpx_topics (
    topic_name   TEXT PRIMARY KEY,
    description  TEXT,
    retention_ms INTEGER DEFAULT 86400000,
    max_size     INTEGER DEFAULT 10000,
    created_at   INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

-- ============================================================
-- hpx_messages: Persisted messages (SQLite bus)
-- ============================================================
CREATE TABLE IF NOT EXISTS hpx_messages (
    message_id   TEXT PRIMARY KEY,
    topic_name   TEXT NOT NULL REFERENCES hpx_topics(topic_name) ON DELETE CASCADE,
    payload      TEXT NOT NULL,
    producer     TEXT,
    created_at   INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    expires_at   INTEGER
);

CREATE INDEX IF NOT EXISTS idx_hpx_messages_topic ON hpx_messages(topic_name, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_hpx_messages_expires ON hpx_messages(expires_at) WHERE expires_at IS NOT NULL;

-- ============================================================
-- hpx_subscriptions: Consumer subscriptions
-- ============================================================
CREATE TABLE IF NOT EXISTS hpx_subscriptions (
    subscription_id TEXT PRIMARY KEY,
    topic_name      TEXT NOT NULL REFERENCES hpx_topics(topic_name) ON DELETE CASCADE,
    consumer_group  TEXT NOT NULL,
    last_message_id TEXT,
    last_ack_at     INTEGER,
    created_at      INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    UNIQUE(topic_name, consumer_group)
);

-- ============================================================
-- hpx_mcp_tools: Dynamic MCP tool registry
-- ============================================================
CREATE TABLE IF NOT EXISTS hpx_mcp_tools (
    tool_name      TEXT PRIMARY KEY,
    tool_category  TEXT NOT NULL,
    description    TEXT NOT NULL,
    input_schema   TEXT NOT NULL,
    handler_type   TEXT NOT NULL CHECK(handler_type IN ('sql_query', 'sql_script', 'endpoint', 'http')),
    handler_config TEXT NOT NULL,
    is_active      INTEGER NOT NULL DEFAULT 1 CHECK(is_active IN (0, 1)),
    version        INTEGER NOT NULL DEFAULT 1,
    created_by     TEXT,
    created_at     INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    updated_at     INTEGER
);

CREATE INDEX IF NOT EXISTS idx_hpx_mcp_tools_active ON hpx_mcp_tools(is_active);

CREATE TRIGGER IF NOT EXISTS trg_hpx_mcp_tools_updated
AFTER UPDATE ON hpx_mcp_tools FOR EACH ROW
BEGIN
    UPDATE hpx_mcp_tools SET updated_at = strftime('%s', 'now') WHERE tool_name = NEW.tool_name;
END;

-- ============================================================
-- Metadata registry (self-describing schema)
-- ============================================================
CREATE TABLE IF NOT EXISTS hpx_metadata (
    table_name  TEXT PRIMARY KEY,
    description TEXT,
    created_at  INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);

INSERT OR IGNORE INTO hpx_metadata (table_name, description) VALUES
    ('hpx_config',              'Key-value configuration store'),
    ('hpx_routes',              'Service routing table (Job as Library)'),
    ('hpx_middleware',          'Ordered middleware chain per route'),
    ('hpx_services',            'Service discovery registry'),
    ('hpx_ratelimits',          'Rate limiting rules'),
    ('hpx_ratelimit_counters',  'Rate limit token counters (ephemeral)'),
    ('hpx_breakers',            'Circuit breaker state per service'),
    ('hpx_certs',               'TLS certificate store'),
    ('hpx_topics',              'Message bus topic registry'),
    ('hpx_messages',            'Persisted bus messages'),
    ('hpx_subscriptions',       'Consumer subscriptions'),
    ('hpx_mcp_tools',           'Dynamic MCP tool registry');
`

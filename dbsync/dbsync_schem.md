╔═══════════════════════════════════════════════════════════════════════════════╗
║  dbsync -- SQLite replication BO->FO via QUIC with filtered snapshots        ║
╠═══════════════════════════════════════════════════════════════════════════════╣
║  Module: github.com/hazyhaar/pkg/dbsync                                     ║
║  Files:  dbsync.go, publisher.go, subscriber.go, quic_transport.go,         ║
║          filter.go, auth_proxy.go (DEPRECATED), proxy.go, health.go,        ║
║          factory.go                                                          ║
║  Deps:   pkg/watch, pkg/auth, pkg/connectivity, quic-go/quic-go            ║
╚═══════════════════════════════════════════════════════════════════════════════╝

FULL REPLICATION FLOW
======================

  BACK-OFFICE (BO)                         FRONT-OFFICE (FO)
  ═══════════════                         ═══════════════════

  ┌──────────────┐                        ┌──────────────────┐
  │  Source DB   │                        │  Read-Only DB    │
  │  (read-write)│                        │  (atomic swap)   │
  └──────┬───────┘                        └────────▲─────────┘
         │                                         │
         │ PRAGMA data_version                     │ rename tmp -> target
         │ poll (200ms)                            │ verify hash + size
         v                                         │
  ┌──────────────┐    QUIC stream           ┌──────┴───────────┐
  │  Publisher   │ ────────────────────────> │  Subscriber      │
  │  .Start(ctx) │   "SYN1" + meta + data   │  .Start(ctx)     │
  │              │                           │  .DB() -> *sql.DB│
  └──────────────┘                           └──────────────────┘
         │
         │ ProduceSnapshot(db, path, filter)
         │   1. VACUUM INTO tmp
         │   2. Drop non-whitelisted tables
         │   3. Apply WHERE filters
         │   4. Truncate columns
         │   5. VACUUM compact
         │   6. SHA-256 hash
         v
  ┌──────────────┐
  │  snapshot.db │  <-- "save chaude" local file
  └──────────────┘

WIRE FORMAT (quic_transport.go)
================================

  ┌──────────┬───────────┬───────────────┬──────────────────────────┐
  │  "SYN1"  │ meta_len  │  meta JSON    │  snapshot bytes          │
  │  4 bytes │ 4B BE u32 │  variable     │  raw or gzip             │
  └──────────┴───────────┴───────────────┴──────────────────────────┘

  ALPN: "horos-dbsync-v1"
  TLS:  1.3 minimum
  QUIC config:
    MaxStreamReceiveWindow = 512 MB
    MaxIdleTimeout = 5 min
    KeepAlive = 30s
    0-RTT disabled

SNAPSHOT PRODUCTION (filter.go)
================================

  ┌─────────────────────────────────────────────────────────┐
  │  ProduceSnapshot(srcDB, dstPath, FilterSpec)            │
  │                                                         │
  │  1. ValidateFilterSpec (reject SQL injection patterns)  │
  │  2. VACUUM INTO tmpPath (consistent full copy)          │
  │  3. Open copy with "sqlite" driver (FK=OFF intentional) │
  │  4. dropUnlisted: DROP tables not in whitelist          │
  │  5. FilteredTables: DELETE FROM t WHERE NOT (clause)    │
  │  6. PartialTables: NULL/zero non-selected columns       │
  │  7. VACUUM (compact)                                    │
  │  8. SHA-256 hash + file size                            │
  │  9. Rename tmp -> dst                                   │
  │                                                         │
  │  Returns *SnapshotMeta{Version, Hash, Size, Timestamp}  │
  └─────────────────────────────────────────────────────────┘

FILTER SPEC
=============

  FilterSpec {
      FullTables      []string                // copied as-is
      FilteredTables  map[string]string        // table -> WHERE clause
      PartialTables   map[string]PartialTable  // table -> cols + optional WHERE
  }

  PartialTable {
      Columns []string  // columns to keep
      Where   string    // optional row filter
  }

  ValidateFilterSpec rejects:
    ; -- /* DROP ALTER CREATE ATTACH DETACH INSERT UPDATE DELETE
    REPLACE UNION INTO EXEC EXECUTE LOAD_EXTENSION PRAGMA

  Column truncation:
    - NOT NULL columns -> zero value (0, 0.0, '')
    - Nullable columns -> NULL

PUBLISHER (publisher.go)
=========================

  NewPublisher(db, targets TargetProvider, filter, savePath, tlsCfg, opts...)
  NewPublisherWithRoutesDB(db, routesDB, filter, savePath, tlsCfg, opts...)
    └── wraps NewRoutesTargetProvider(routesDB)

  Start(ctx) error        -- blocks, watches via pkg/watch
  Ping(ctx) error         -- verify source DB accessible
  LastMeta() *SnapshotMeta
  Status() map[string]any

  Publish logic:
    1. ProduceSnapshot(db, savePath, filter)
    2. Dedup: skip if hash unchanged (lastHash comparison)
    3. targets.Targets(ctx) -> []Target
    4. Push to all targets in parallel (goroutines + WaitGroup)
    5. Skip "noop" strategy targets

SUBSCRIBER (subscriber.go)
============================

  NewSubscriber(dbPath, listenAddr, tlsCfg, opts...)

  Start(ctx) error          -- blocks, ListenSnapshots on QUIC
  DB() *sql.DB              -- current read-only DB (may be nil)
  OnSwap(fn func())         -- callback after successful swap
  Ping(ctx) error           -- nil if snapshot received + DB ok
  Version() int64           -- last snapshot version
  LastSwapAt() int64        -- unix timestamp
  StaleSince() time.Duration
  Status() map[string]any
  Close() error

  handleSnapshot flow:
    1. MaxAge validation (reject stale/replay snapshots)
    2. Write to dbPath + ".incoming" temp file
    3. SHA-256 hash while writing (TeeReader)
    4. Verify size match + hash match
    5. Atomic swap: close old DB -> rename tmp -> open new read-only
    6. Fire OnSwap callbacks

  openReadOnly: file:path?mode=ro&_pragma=journal_mode(WAL)
                &_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)

TARGET PROVIDERS (dbsync.go)
==============================

  TargetProvider interface {
      Targets(ctx) ([]Target, error)
  }

  Target { Name, Strategy, Endpoint string }

  StaticTargetProvider   -- fixed list, NewStaticTargetProvider(targets...)
  RoutesTargetProvider   -- reads connectivity routes table
    WHERE strategy IN ('dbsync','noop') AND service_name LIKE 'dbsync:%'

TLS CONFIGURATIONS (quic_transport.go)
=======================================

  SyncTLSConfig(certFile, keyFile)                          -> server-side
  SyncClientTLSConfig(insecureSkipVerify)                   -> dev client
  SyncClientTLSConfigWithCA(caCertFile)                     -> prod client
  SyncTLSConfigMutual(certFile, keyFile, caCertFile)        -> mutual auth

  All: ALPN="horos-dbsync-v1", TLS 1.3 minimum

WRITE PROXY (proxy.go)
========================

  ┌──────────┐   POST/PUT/DELETE    ┌──────────────┐     ┌──────────┐
  │  FO user │ ──────────────────>  │  WriteProxy   │ ──> │  BO API  │
  └──────────┘                      │  (reverse prx)│     └──────────┘
                                    └──────────────┘

  NewWriteProxy(boEndpoint, tlsCfg) (*WriteProxy, error)
  (p) Handler() http.Handler       -- httputil.ReverseProxy
  RedirectHandler(boURL) http.HandlerFunc  -- 307 redirect alternative

BO HEALTH CHECKER (health.go)
==============================

  ┌───────────────────────────────────┐
  │  BOHealthChecker                  │
  │                                   │
  │  Pings boURL+"/health" every N    │
  │  Caches result in atomic.Bool     │
  │                                   │
  │  Healthy() bool                   │
  │  Status() map[string]any          │
  │    reachable, last_check,         │
  │    latency_ms, check_count,       │
  │    fail_count                     │
  └───────────────────────────────────┘

  NewBOHealthChecker(boURL, interval) -- default interval 10s
  Start(ctx) -- blocks

AUTH PROXY (auth_proxy.go) -- DEPRECATED
=========================================

  !! Use authproxy.NewAuthProxy instead !!

  AuthProxy { boURL, cookieDomain, secure }

  LoginHandler(setFlash)          -> POST /login
  RegisterHandler(setFlash)       -> POST /register
  ForgotPasswordHandler(setFlash) -> POST /forgot-password
  ResetPasswordHandler(setFlash)  -> POST /reset-password

  Flow: FO form -> callBO(path, JSON) -> BO internal API
        -> set cookie (auth.SetTokenCookie) -> redirect

  Has optional HealthCheck func for circuit breaker pattern.

CONNECTIVITY FACTORIES (factory.go)
=====================================

  DBSyncFactory(pub *Publisher) connectivity.TransportFactory
    router.Call(ctx, "dbsync:fo-1", nil) -> publisher status JSON

  SubscriberFactory(sub *Subscriber) connectivity.TransportFactory
    router.Call(ctx, "dbsync:sub", nil) -> subscriber status JSON

OPTIONS
========

  WithWatchInterval(d)    -- DB polling interval (default 200ms)
  WithWatchDebounce(d)    -- debounce after change (default 200ms)
  WithCompression()       -- gzip snapshots (60-70% size reduction)
  WithMaxAge(d)           -- reject snapshots older than d
  WithDriverName(name)    -- SQL driver (default "sqlite")

CONSTANTS
==========

  ALPNProtocol     = "horos-dbsync-v1"
  MagicBytes       = "SYN1"
  MaxSnapshotSize  = 512 MB

SNAPSHOT META
==============

  SnapshotMeta {
      Version    int64   // unix milliseconds
      Hash       string  // SHA-256 hex
      Size       int64   // uncompressed bytes
      Timestamp  int64   // unix seconds
      Compressed bool    // gzip?
  }

EXPORTED TYPES SUMMARY
=======================

  Publisher, Subscriber, FilterSpec, PartialTable, SnapshotMeta,
  Target, TargetProvider, StaticTargetProvider, RoutesTargetProvider,
  WriteProxy, BOHealthChecker, AuthProxy (deprecated), Option

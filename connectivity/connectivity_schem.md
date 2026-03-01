╔════════════════════════════════════════════════════════════════════════════════╗
║  connectivity -- Smart service router: local or remote dispatch via SQLite    ║
╠════════════════════════════════════════════════════════════════════════════════╣
║  Module: github.com/hazyhaar/pkg/connectivity                                ║
║  Files:  router.go, schema.go, watcher.go, admin.go, breaker.go,            ║
║          retry.go, middleware.go, factory_http.go, factory_mcp.go,           ║
║          observe.go, inspect.go, fallback.go, gateway.go, errors.go         ║
║  Deps:   pkg/dbopen, pkg/mcpquic, pkg/horosafe, pkg/observability           ║
╚════════════════════════════════════════════════════════════════════════════════╝

ARCHITECTURE -- "Job as Library" pattern
=========================================

  ┌────────────┐     ┌──────────────────────────────────────────────────┐
  │  Caller    │     │                   Router                         │
  │            │     │                                                  │
  │ Call(ctx,  ├────>│  1. noop?   -> return nil, nil                   │
  │  "billing",│     │  2. remote? -> remoteEntries[svc].handler(...)   │
  │  payload)  │     │  3. local?  -> localHandlers[svc](ctx, payload)  │
  │            │     │  4. none?   -> ErrServiceNotFound                │
  └────────────┘     └──────────────────────────────────────────────────┘
                                        ^
                                        │ Reload() reads routes table
                                        │
                     ┌──────────────────────────────────────────────────┐
                     │           SQLite routes table                     │
                     │  ┌────────────┬──────────┬───────────┬──────────┐│
                     │  │service_name│ strategy │ endpoint  │ config   ││
                     │  │ TEXT (PK)  │ TEXT     │ TEXT      │ TEXT/JSON││
                     │  ├────────────┼──────────┼───────────┼──────────┤│
                     │  │ billing    │ local    │           │ {}       ││
                     │  │ search     │ quic     │ 10.0.0.5  │ {..}    ││
                     │  │ embed      │ http     │ http://.. │ {..}    ││
                     │  │ disabled   │ noop     │           │ {}       ││
                     │  └────────────┴──────────┴───────────┴──────────┘│
                     └──────────────────────────────────────────────────┘

HOT-RELOAD LOOP (watcher.go)
==============================

  ┌─────────────────┐   PRAGMA data_version   ┌──────────────┐
  │  Watch(ctx, db, ├──────── poll 200ms ────> │  SQLite DB   │
  │  200ms)         │                          └──────┬───────┘
  └────────┬────────┘                                 │
           │  version changed?                        │
           │  YES -> Reload(ctx, db)                  │
           │         - diff fingerprints              │
           │         - close stale handlers           │
           │         - build new via factories        │
           └──────────────────────────────────────────┘

  fingerprint = strategy + "|" + endpoint + "|" + config
  Only changed routes are rebuilt. Unchanged routes keep their connections.

DATABASE SCHEMA (schema.go)
============================

  CREATE TABLE IF NOT EXISTS routes (
      service_name TEXT PRIMARY KEY,
      strategy     TEXT NOT NULL CHECK(strategy IN
                       ('local','quic','http','mcp','dbsync','embed','noop')),
      endpoint     TEXT,
      config       TEXT DEFAULT '{}',
      updated_at   INTEGER NOT NULL DEFAULT (strftime('%s','now'))
  );
  CREATE INDEX idx_routes_strategy ON routes(strategy);
  CREATE TRIGGER trg_routes_updated_at AFTER UPDATE ON routes ...;

  OpenDB(path) -> dbopen.Open(path, WithBusyTimeout(5000))
  Init(db)     -> db.Exec(Schema)

TRANSPORT FACTORIES
====================

  ┌────────────────────────────────────────────────────────────────────┐
  │  RegisterTransport("http",  HTTPFactory())                        │
  │  RegisterTransport("http",  HTTPFactory(AllowInternal()))         │
  │  RegisterTransport("mcp",   MCPFactory())                         │
  │  RegisterTransport("quic",  ...)  // user-provided                │
  │  RegisterTransport("dbsync", dbsync.DBSyncFactory(pub))          │
  │  RegisterTransport("embed",  horosembed.EmbedFactory())          │
  └────────────────────────────────────────────────────────────────────┘

  HTTPFactory (factory_http.go):
    endpoint  -> POST payload to URL
    config    -> {"timeout_ms": N, "content_type": "..."}
    SSRF guard via horosafe.ValidateURL (unless AllowInternal())
    Response cap: 10 MiB (maxHTTPResponseBody)
    close()   -> client.CloseIdleConnections()

  MCPFactory (factory_mcp.go):
    endpoint  -> QUIC address "ip:port"
    config    -> {"tool_name": "...", "insecure_tls": bool}
    Eager connect on Reload (fail fast)
    Payload unmarshalled as JSON map -> CallTool(ctx, toolName, args)
    close()   -> client.Close()

GATEWAY (gateway.go)
=====================

  ┌──────────────────┐    POST /{service}     ┌─────────────────────┐
  │  Remote caller   │ ──────────────────────> │  router.Gateway()   │
  │  (HTTPFactory)   │                         │  http.Handler       │
  └──────────────────┘                         │                     │
                                               │  dispatch to LOCAL  │
                                               │  handlers ONLY      │
                                               │  (no remote->remote)│
                                               │  Body cap: 16 MiB   │
                                               └─────────────────────┘

  Mount: r.Mount("/connectivity", router.Gateway())

ADMIN (admin.go) -- CRUD on routes table
==========================================

  Admin { db *sql.DB }
  NewAdmin(db) -> *Admin

  ListRoutes(ctx)                        -> []RouteRow
  GetRoute(ctx, serviceName)             -> *RouteRow
  UpsertRoute(ctx, name, strat, ep, cfg) -> error
  DeleteRoute(ctx, serviceName)          -> error
  SetStrategy(ctx, name, strategy)       -> error  (quick enable/disable)

  RouteRow { ServiceName, Strategy, Endpoint, Config json.RawMessage, UpdatedAt }

  All mutations go through SQLite -> Watch detects via data_version.

MIDDLEWARE CHAIN (middleware.go, retry.go, breaker.go, observe.go, fallback.go)
================================================================================

  Handler = func(ctx, []byte) ([]byte, error)
  HandlerMiddleware = func(Handler) Handler

  ┌─────────────────────────────────────────────────────────────────┐
  │  Chain(mws...) -- compose left-to-right                        │
  │                                                                 │
  │  Logging(logger)                  -- log duration + error       │
  │  Timeout(d)                       -- context.WithTimeout        │
  │  Recovery(logger)                 -- panic -> ErrPanic          │
  │  WithTimeout(defaultTimeout)      -- timeout from config        │
  │  WithRetry(max, backoff, logger)  -- exp backoff, skip circuit  │
  │  WithCircuitBreaker(cb, svc)      -- ErrCircuitOpen on open     │
  │  WithObservability(mm, svc, strat)-- metrics + error count      │
  │  WithCallLogging(logger, svc)     -- slog structured logging    │
  │  WithFallback(local, svc, logger) -- remote fail -> try local   │
  └─────────────────────────────────────────────────────────────────┘

  Example chain:
    Chain(Recovery(log), Logging(log), WithRetry(3, 100ms, log),
          WithCircuitBreaker(cb, "billing"))

CIRCUIT BREAKER (breaker.go) -- inline copy for connectivity
=============================================================

  CircuitBreaker (same state machine as circuitbreaker pkg, no persistence)

  States: BreakerClosed(0) -> BreakerOpen(1) -> BreakerHalfOpen(2)
  Defaults: threshold=5, resetTimeout=30s, halfOpenMax=2

  WithCircuitBreaker(cb, svc) HandlerMiddleware
    - cb.Allow()? -> NO -> ErrCircuitOpen{Service}
    - YES -> next(ctx, payload) -> RecordSuccess/RecordFailure

INSPECTION (inspect.go)
========================

  ServiceInfo { Name, Strategy, Endpoint, HasLocal }

  ListServices() iter.Seq[ServiceInfo]  -- all known services (remote + local)
  Inspect(service) (ServiceInfo, bool)  -- single service lookup

ERROR TYPES (errors.go)
========================

  ErrServiceNotFound { Service }
  ErrNoFactory       { Service, Strategy }
  ErrFactoryFailed   { Service, Strategy, Endpoint, Cause }  (Unwrap)
  ErrCallTimeout     { Service }
  ErrCircuitOpen     { Service }
  ErrPanic           { Value any }

CORE TYPES SUMMARY
===================

  Handler            func(ctx, []byte) ([]byte, error)
  TransportFactory   func(endpoint, config) (Handler, close func(), error)
  HandlerMiddleware  func(Handler) Handler
  Router             { localHandlers, remoteEntries, routeSnap, factories }
  Admin              { db *sql.DB }
  CircuitBreaker     { state, failures, successes, threshold, ... }
  RouteRow           { ServiceName, Strategy, Endpoint, Config, UpdatedAt }
  ServiceInfo        { Name, Strategy, Endpoint, HasLocal }

KEY FUNCTIONS (simplified)
===========================

  New(opts...) *Router
  (r) RegisterLocal(service, Handler)
  (r) RegisterTransport(protocol, TransportFactory)
  (r) Call(ctx, service, payload) ([]byte, error)
  (r) Reload(ctx, db) error
  (r) Watch(ctx, db, interval)          -- blocking, run in goroutine
  (r) Close() error
  (r) Gateway() http.Handler
  (r) ListServices() iter.Seq[ServiceInfo]
  (r) Inspect(service) (ServiceInfo, bool)
  OpenDB(path) (*sql.DB, error)
  Init(db) error
  NewAdmin(db) *Admin
  HTTPFactory(opts...) TransportFactory
  MCPFactory() TransportFactory
  Chain(mws...) HandlerMiddleware

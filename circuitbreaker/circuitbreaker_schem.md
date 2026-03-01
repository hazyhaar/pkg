╔═══════════════════════════════════════════════════════════════════════════╗
║  circuitbreaker -- Transport-agnostic circuit breaker state machine      ║
╠═══════════════════════════════════════════════════════════════════════════╣
║  Module: github.com/hazyhaar/pkg/circuitbreaker                         ║
║  Files:  circuitbreaker.go                                              ║
║  Deps:   stdlib only (context, database/sql, fmt, sync, time)           ║
╚═══════════════════════════════════════════════════════════════════════════╝

STATE MACHINE
=============

                    failures >= threshold
  ┌────────┐  ──────────────────────────────>  ┌────────┐
  │ CLOSED │                                   │  OPEN  │
  │        │  <──────────────────────────────   │        │
  └────┬───┘    successes >= halfOpenMax        └───┬────┘
       │         (via HalfOpen)                     │
       │                                            │
       │    ┌────────────────┐     resetTimeout     │
       └──> │   HALF-OPEN    │ <────────────────────┘
            │  (probe calls) │     elapsed
            └────────────────┘
                 │
                 │ any failure -> back to OPEN
                 v

  CLOSED:    Normal. Calls pass through. RecordSuccess resets failure count.
  OPEN:      Calls rejected immediately (ErrOpen). Timer running.
  HALF-OPEN: After resetTimeout expires, limited probes allowed.
             N successes (halfOpenMax) -> CLOSED.
             Any failure -> back to OPEN.

FLOW
====

  caller ──> Execute(ctx, fn)
                │
                ├── Allow()? ── NO ──> return ErrOpen{Name}
                │
                ├── YES ──> fn()
                │             │
                │             ├── err == nil ──> RecordSuccess()
                │             │
                │             └── err != nil ──> RecordFailure()
                │
                └── return fn's result or ErrOpen

CONFIGURATION (defaults)
========================

  ┌──────────────────────────────────────────────────┐
  │  threshold    = 5       failures to trip OPEN     │
  │  resetTimeout = 30s     OPEN duration before probe│
  │  halfOpenMax  = 2       successes to close         │
  │  now          = time.Now  injectable clock         │
  │  store        = nil       optional SQLite backend  │
  └──────────────────────────────────────────────────┘

  Options (functional pattern):
    WithThreshold(n int)
    WithResetTimeout(d time.Duration)
    WithHalfOpenMax(n int)
    WithClock(fn func() time.Time)
    WithSQLite(db *sql.DB)

PERSISTENCE (optional SQLite)
=============================

  ┌──────────────────────────────────────────────────────────────┐
  │  TABLE circuit_breakers                                      │
  │  ┌─────────────┬──────────┬──────────────┬──────────────────┐│
  │  │ name (PK)   │ state    │ failures     │ last_failure     ││
  │  │ TEXT        │ INTEGER  │ INTEGER      │ INTEGER (unix)   ││
  │  ├─────────────┼──────────┼──────────────┼──────────────────┤│
  │  │             │          │              │ updated_at INT   ││
  │  └─────────────┴──────────┴──────────────┴──────────────────┘│
  │                                                              │
  │  Init()  -> CREATE TABLE IF NOT EXISTS                       │
  │  Load(n) -> SELECT state, failures, last_failure             │
  │  Save(n) -> INSERT ... ON CONFLICT DO UPDATE                 │
  └──────────────────────────────────────────────────────────────┘

  State is loaded from SQLite on New() if store is configured.
  State is persisted on every transition (Open, Closed from HalfOpen).
  RecordSuccess in Closed state does NOT persist (just resets counter).

EXPORTED TYPES
==============

  State (int)
    Closed   = 0
    Open     = 1
    HalfOpen = 2
    .String() -> "closed" | "open" | "half-open"

  ErrOpen { Name string }
    Returned by Execute() / checked by Allow()

  Breaker {
    Name()          string
    State()         State        // applies time-based transitions
    Allow()         bool         // false if Open and timeout not elapsed
    RecordSuccess()              // HalfOpen: count toward close. Closed: reset failures.
    RecordFailure()              // Closed: count toward open. HalfOpen: back to open.
    Reset()                      // force Closed
    Execute(ctx, fn) error       // Allow + fn + Record in one call
  }

  Store (interface)
    Init() error
    Load(name) (State, int, time.Time, error)
    Save(name, State, int, time.Time) error

  Option func(*Breaker)

KEY FUNCTIONS (simplified signatures)
=====================================

  New(name string, opts ...Option) *Breaker
  (b *Breaker) Execute(ctx context.Context, fn func() error) error
  (b *Breaker) Allow() bool
  (b *Breaker) RecordSuccess()
  (b *Breaker) RecordFailure()
  (b *Breaker) Reset()
  (b *Breaker) State() State
  (b *Breaker) Name() string

THREAD SAFETY
=============

  All methods use sync.Mutex. Safe for concurrent use.
  maybeTransition() is called inside Allow()/State() under lock.

DIFFERENCE FROM connectivity.CircuitBreaker
=============================================

  This package (circuitbreaker) is standalone with optional SQLite persistence.
  connectivity.CircuitBreaker is an inline copy without persistence,
  integrated directly into the connectivity middleware chain.

NO HTTP ROUTES, NO MIDDLEWARE

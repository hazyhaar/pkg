// CLAUDE:SUMMARY Transport-agnostic circuit breaker with closed/open/half-open states and optional SQLite persistence.
// CLAUDE:DEPENDS
// CLAUDE:EXPORTS State, Closed, Open, HalfOpen, ErrOpen, Breaker, Option, Store, New, WithThreshold, WithResetTimeout, WithHalfOpenMax, WithSQLite

// Package circuitbreaker provides a transport-agnostic circuit breaker state
// machine. It can be used with any call pattern: HTTP, MCP, QUIC, or internal
// function calls.
//
// The breaker transitions through three states:
//   - Closed: normal operation, calls pass through.
//   - Open: calls are rejected immediately with ErrOpen.
//   - HalfOpen: a limited number of probe calls are allowed to test recovery.
//
// State is stored in-memory by default, with an optional SQLite backend
// for persistence across restarts.
package circuitbreaker

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// State represents the circuit breaker state.
type State int

const (
	Closed   State = iota // Normal operation, calls pass through.
	Open                  // Calls rejected immediately.
	HalfOpen              // Probe calls allowed to test recovery.
)

// String returns the state name.
func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// ErrOpen is returned when the circuit breaker is open and calls are rejected.
type ErrOpen struct {
	Name string
}

func (e *ErrOpen) Error() string {
	return fmt.Sprintf("circuitbreaker: %s is open", e.Name)
}

// Breaker implements the circuit breaker pattern.
// Thread-safe: all state transitions use a mutex.
type Breaker struct {
	mu           sync.Mutex
	name         string
	state        State
	failures     int
	successes    int
	threshold    int           // failures before opening
	resetTimeout time.Duration // how long to stay open before half-open
	halfOpenMax  int           // successes in half-open before closing
	lastFailure  time.Time
	now          func() time.Time // injectable clock for testing
	store        Store            // optional persistence backend
}

// Option configures a Breaker.
type Option func(*Breaker)

// WithThreshold sets the failure count that trips the breaker open (default 5).
func WithThreshold(n int) Option {
	return func(b *Breaker) { b.threshold = n }
}

// WithResetTimeout sets how long the breaker stays open before transitioning
// to half-open (default 30s).
func WithResetTimeout(d time.Duration) Option {
	return func(b *Breaker) { b.resetTimeout = d }
}

// WithHalfOpenMax sets how many consecutive successes in half-open are needed
// to close the breaker (default 2).
func WithHalfOpenMax(n int) Option {
	return func(b *Breaker) { b.halfOpenMax = n }
}

// WithClock sets a custom clock function (for testing).
func WithClock(fn func() time.Time) Option {
	return func(b *Breaker) { b.now = fn }
}

// WithSQLite enables SQLite-backed persistence for the breaker state.
// The state is loaded on creation and persisted on every transition.
func WithSQLite(db *sql.DB) Option {
	return func(b *Breaker) { b.store = &sqliteStore{db: db} }
}

// Defaults returns a set of options with the standard defaults.
func Defaults() Option {
	return func(b *Breaker) {
		// Already set in New, this is a no-op — provided for explicit intent.
	}
}

// New creates a Breaker with sensible defaults:
// 5 failures to open, 30s reset timeout, 2 successes to close from half-open.
func New(name string, opts ...Option) *Breaker {
	b := &Breaker{
		name:         name,
		state:        Closed,
		threshold:    5,
		resetTimeout: 30 * time.Second,
		halfOpenMax:  2,
		now:          time.Now,
	}
	for _, o := range opts {
		o(b)
	}
	// Load persisted state if store is configured.
	if b.store != nil {
		if err := b.store.Init(); err == nil {
			if s, f, lf, err := b.store.Load(name); err == nil {
				b.state = s
				b.failures = f
				b.lastFailure = lf
			}
		}
	}
	return b
}

// Name returns the breaker name.
func (b *Breaker) Name() string {
	return b.name
}

// State returns the current breaker state, applying time-based transitions.
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maybeTransition()
	return b.state
}

// Allow checks whether a call is allowed. Returns false if the breaker
// is open and the reset timeout has not elapsed.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maybeTransition()
	return b.state != Open
}

// RecordSuccess records a successful call.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maybeTransition()
	switch b.state {
	case HalfOpen:
		b.successes++
		if b.successes >= b.halfOpenMax {
			b.state = Closed
			b.failures = 0
			b.successes = 0
			b.persist()
		}
	case Closed:
		b.failures = 0
	}
}

// RecordFailure records a failed call.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.maybeTransition()
	b.lastFailure = b.now()
	switch b.state {
	case Closed:
		b.failures++
		if b.failures >= b.threshold {
			b.state = Open
			b.persist()
		}
	case HalfOpen:
		// Any failure in half-open goes back to open.
		b.state = Open
		b.successes = 0
		b.persist()
	}
}

// Reset forces the breaker back to closed state.
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = Closed
	b.failures = 0
	b.successes = 0
	b.persist()
}

// Execute runs fn if the breaker allows it, recording success or failure.
// Returns ErrOpen if the breaker is open.
func (b *Breaker) Execute(ctx context.Context, fn func() error) error {
	if !b.Allow() {
		return &ErrOpen{Name: b.name}
	}
	if err := fn(); err != nil {
		b.RecordFailure()
		return err
	}
	b.RecordSuccess()
	return nil
}

// maybeTransition checks if an open breaker should move to half-open.
// Must be called with mu held.
func (b *Breaker) maybeTransition() {
	if b.state == Open && b.now().Sub(b.lastFailure) >= b.resetTimeout {
		b.state = HalfOpen
		b.successes = 0
	}
}

// persist saves state to the store if configured.
func (b *Breaker) persist() {
	if b.store != nil {
		_ = b.store.Save(b.name, b.state, b.failures, b.lastFailure)
	}
}

// --- Persistence interface ---

// Store is the interface for circuit breaker state persistence.
type Store interface {
	Init() error
	Load(name string) (state State, failures int, lastFailure time.Time, err error)
	Save(name string, state State, failures int, lastFailure time.Time) error
}

// --- SQLite store ---

const storeSchema = `
CREATE TABLE IF NOT EXISTS circuit_breakers (
	name TEXT PRIMARY KEY,
	state INTEGER NOT NULL DEFAULT 0,
	failures INTEGER NOT NULL DEFAULT 0,
	last_failure INTEGER NOT NULL DEFAULT 0,
	updated_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
);
`

type sqliteStore struct {
	db *sql.DB
}

func (s *sqliteStore) Init() error {
	_, err := s.db.Exec(storeSchema)
	return err
}

func (s *sqliteStore) Load(name string) (State, int, time.Time, error) {
	var stateInt, failures int
	var lastFailureUnix int64
	err := s.db.QueryRow(
		`SELECT state, failures, last_failure FROM circuit_breakers WHERE name = ?`, name,
	).Scan(&stateInt, &failures, &lastFailureUnix)
	if err != nil {
		return Closed, 0, time.Time{}, err
	}
	return State(stateInt), failures, time.Unix(lastFailureUnix, 0), nil
}

func (s *sqliteStore) Save(name string, state State, failures int, lastFailure time.Time) error {
	_, err := s.db.Exec(`INSERT INTO circuit_breakers (name, state, failures, last_failure)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET state=excluded.state, failures=excluded.failures,
		last_failure=excluded.last_failure, updated_at=strftime('%s','now')`,
		name, int(state), failures, lastFailure.Unix())
	return err
}

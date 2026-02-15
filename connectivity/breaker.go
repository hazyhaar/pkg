package connectivity

import (
	"context"
	"sync"
	"time"
)

// BreakerState represents the circuit breaker state.
type BreakerState int

const (
	BreakerClosed   BreakerState = iota // Normal operation, calls pass through.
	BreakerOpen                         // Calls rejected immediately.
	BreakerHalfOpen                     // One probe call allowed to test recovery.
)

// CircuitBreaker implements the circuit breaker pattern per service.
// Thread-safe: all state transitions use a mutex.
type CircuitBreaker struct {
	mu           sync.Mutex
	state        BreakerState
	failures     int
	successes    int
	threshold    int           // failures before opening
	resetTimeout time.Duration // how long to stay open before half-open
	halfOpenMax  int           // successes in half-open before closing
	lastFailure  time.Time
	now          func() time.Time // injectable clock for testing
}

// BreakerOption configures a CircuitBreaker.
type BreakerOption func(*CircuitBreaker)

// WithBreakerThreshold sets the failure count that trips the breaker open.
func WithBreakerThreshold(n int) BreakerOption {
	return func(cb *CircuitBreaker) { cb.threshold = n }
}

// WithBreakerResetTimeout sets how long the breaker stays open before
// transitioning to half-open.
func WithBreakerResetTimeout(d time.Duration) BreakerOption {
	return func(cb *CircuitBreaker) { cb.resetTimeout = d }
}

// WithBreakerHalfOpenMax sets how many consecutive successes in half-open
// are needed to close the breaker.
func WithBreakerHalfOpenMax(n int) BreakerOption {
	return func(cb *CircuitBreaker) { cb.halfOpenMax = n }
}

// WithBreakerClock sets a custom clock function (for testing).
func WithBreakerClock(fn func() time.Time) BreakerOption {
	return func(cb *CircuitBreaker) { cb.now = fn }
}

// NewCircuitBreaker creates a breaker with sensible defaults:
// 5 failures to open, 30s reset timeout, 2 successes to close from half-open.
func NewCircuitBreaker(opts ...BreakerOption) *CircuitBreaker {
	cb := &CircuitBreaker{
		state:        BreakerClosed,
		threshold:    5,
		resetTimeout: 30 * time.Second,
		halfOpenMax:  2,
		now:          time.Now,
	}
	for _, o := range opts {
		o(cb)
	}
	return cb
}

// State returns the current breaker state.
func (cb *CircuitBreaker) State() BreakerState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.maybeTransition()
	return cb.state
}

// Allow checks whether a call is allowed. Returns false if the breaker
// is open and the reset timeout has not elapsed.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.maybeTransition()
	return cb.state != BreakerOpen
}

// RecordSuccess records a successful call.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case BreakerHalfOpen:
		cb.successes++
		if cb.successes >= cb.halfOpenMax {
			cb.state = BreakerClosed
			cb.failures = 0
			cb.successes = 0
		}
	case BreakerClosed:
		cb.failures = 0
	}
}

// RecordFailure records a failed call.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.lastFailure = cb.now()
	switch cb.state {
	case BreakerClosed:
		cb.failures++
		if cb.failures >= cb.threshold {
			cb.state = BreakerOpen
		}
	case BreakerHalfOpen:
		// Any failure in half-open goes back to open.
		cb.state = BreakerOpen
		cb.successes = 0
	}
}

// Reset forces the breaker back to closed state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = BreakerClosed
	cb.failures = 0
	cb.successes = 0
}

// maybeTransition checks if an open breaker should move to half-open.
// Must be called with mu held.
func (cb *CircuitBreaker) maybeTransition() {
	if cb.state == BreakerOpen && cb.now().Sub(cb.lastFailure) >= cb.resetTimeout {
		cb.state = BreakerHalfOpen
		cb.successes = 0
	}
}

// WithCircuitBreaker returns a HandlerMiddleware that wraps calls with
// a circuit breaker. When the breaker is open, calls are rejected
// immediately with ErrCircuitOpen.
func WithCircuitBreaker(cb *CircuitBreaker, service string) HandlerMiddleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, payload []byte) ([]byte, error) {
			if !cb.Allow() {
				return nil, &ErrCircuitOpen{Service: service}
			}
			resp, err := next(ctx, payload)
			if err != nil {
				cb.RecordFailure()
			} else {
				cb.RecordSuccess()
			}
			return resp, err
		}
	}
}

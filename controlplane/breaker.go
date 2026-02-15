package controlplane

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// BreakerEntry represents a row from the hpx_breakers table.
type BreakerEntry struct {
	ServiceName     string `json:"service_name"`
	State           string `json:"state"`
	FailureCount    int    `json:"failure_count"`
	SuccessCount    int    `json:"success_count"`
	Threshold       int    `json:"threshold"`
	ResetTimeoutMs  int64  `json:"reset_timeout_ms"`
	LastFailure     int64  `json:"last_failure,omitempty"`
	LastStateChange int64  `json:"last_state_change"`
}

// GetBreaker returns the circuit breaker state for a service, or nil.
func (cp *ControlPlane) GetBreaker(ctx context.Context, serviceName string) (*BreakerEntry, error) {
	var b BreakerEntry
	var lastFailure sql.NullInt64
	err := cp.db.QueryRowContext(ctx,
		`SELECT service_name, state, failure_count, success_count, threshold,
		        reset_timeout_ms, last_failure, last_state_change
		 FROM hpx_breakers WHERE service_name = ?`, serviceName).
		Scan(&b.ServiceName, &b.State, &b.FailureCount, &b.SuccessCount,
			&b.Threshold, &b.ResetTimeoutMs, &lastFailure, &b.LastStateChange)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("controlplane: get breaker: %w", err)
	}
	if lastFailure.Valid {
		b.LastFailure = lastFailure.Int64
	}
	return &b, nil
}

// UpsertBreaker inserts or updates a circuit breaker entry.
func (cp *ControlPlane) UpsertBreaker(ctx context.Context, b BreakerEntry) error {
	_, err := cp.db.ExecContext(ctx,
		`INSERT INTO hpx_breakers (service_name, state, failure_count, success_count, threshold, reset_timeout_ms, last_failure)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(service_name) DO UPDATE SET
		     state = excluded.state,
		     failure_count = excluded.failure_count,
		     success_count = excluded.success_count,
		     threshold = excluded.threshold,
		     reset_timeout_ms = excluded.reset_timeout_ms,
		     last_failure = excluded.last_failure,
		     last_state_change = ?`,
		b.ServiceName, b.State, b.FailureCount, b.SuccessCount,
		b.Threshold, b.ResetTimeoutMs, b.LastFailure, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("controlplane: upsert breaker: %w", err)
	}
	return nil
}

// ResetBreaker forces a circuit breaker back to the closed state.
func (cp *ControlPlane) ResetBreaker(ctx context.Context, serviceName string) error {
	_, err := cp.db.ExecContext(ctx,
		`UPDATE hpx_breakers SET state = 'closed', failure_count = 0, success_count = 0,
		        last_state_change = ? WHERE service_name = ?`,
		time.Now().Unix(), serviceName)
	return err
}

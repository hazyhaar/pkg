package controlplane

import (
	"context"
	"fmt"
)

// RateLimitRule represents a row from the hpx_ratelimits table.
type RateLimitRule struct {
	RuleID      string `json:"rule_id"`
	ServiceName string `json:"service_name,omitempty"`
	Scope       string `json:"scope"`
	MaxRequests int    `json:"max_requests"`
	WindowMs    int64  `json:"window_ms"`
	Burst       int    `json:"burst"`
	Enabled     bool   `json:"enabled"`
}

// UpsertRateLimit inserts or updates a rate limit rule.
func (cp *ControlPlane) UpsertRateLimit(ctx context.Context, r RateLimitRule) error {
	enabled := 0
	if r.Enabled {
		enabled = 1
	}
	_, err := cp.db.ExecContext(ctx,
		`INSERT INTO hpx_ratelimits (rule_id, service_name, scope, max_requests, window_ms, burst, enabled)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(rule_id) DO UPDATE SET
		     service_name = excluded.service_name,
		     scope = excluded.scope,
		     max_requests = excluded.max_requests,
		     window_ms = excluded.window_ms,
		     burst = excluded.burst,
		     enabled = excluded.enabled`,
		r.RuleID, r.ServiceName, r.Scope, r.MaxRequests, r.WindowMs, r.Burst, enabled)
	if err != nil {
		return fmt.Errorf("controlplane: upsert ratelimit: %w", err)
	}
	return nil
}

// DeleteRateLimit removes a rate limit rule by ID.
func (cp *ControlPlane) DeleteRateLimit(ctx context.Context, ruleID string) error {
	_, err := cp.db.ExecContext(ctx,
		`DELETE FROM hpx_ratelimits WHERE rule_id = ?`, ruleID)
	return err
}

// GetRateLimitsForService returns all active rate limit rules for a service.
func (cp *ControlPlane) GetRateLimitsForService(ctx context.Context, serviceName string) ([]RateLimitRule, error) {
	rows, err := cp.db.QueryContext(ctx,
		`SELECT rule_id, COALESCE(service_name,''), scope, max_requests, window_ms, burst, enabled
		 FROM hpx_ratelimits
		 WHERE enabled = 1 AND (service_name = ? OR scope = 'global')
		 ORDER BY scope`, serviceName)
	if err != nil {
		return nil, fmt.Errorf("controlplane: get ratelimits: %w", err)
	}
	defer rows.Close()

	var rules []RateLimitRule
	for rows.Next() {
		var r RateLimitRule
		var enabled int
		if err := rows.Scan(&r.RuleID, &r.ServiceName, &r.Scope, &r.MaxRequests, &r.WindowMs, &r.Burst, &enabled); err != nil {
			return nil, fmt.Errorf("controlplane: scan ratelimit: %w", err)
		}
		r.Enabled = enabled == 1
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

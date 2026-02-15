package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"iter"
)

// Route represents a row from the hpx_routes table.
type Route struct {
	ServiceName string          `json:"service_name"`
	Strategy    string          `json:"strategy"`
	Endpoint    string          `json:"endpoint,omitempty"`
	Config      json.RawMessage `json:"config,omitempty"`
	Priority    int             `json:"priority"`
	Enabled     bool            `json:"enabled"`
	UpdatedAt   int64           `json:"updated_at"`
}

// GetRoute returns a single route by service name, or nil if not found.
func (cp *ControlPlane) GetRoute(ctx context.Context, serviceName string) (*Route, error) {
	var r Route
	var cfgStr string
	var enabled int
	err := cp.db.QueryRowContext(ctx,
		`SELECT service_name, strategy, COALESCE(endpoint,''), COALESCE(config,'{}'),
		        priority, enabled, updated_at
		 FROM hpx_routes WHERE service_name = ?`, serviceName).
		Scan(&r.ServiceName, &r.Strategy, &r.Endpoint, &cfgStr, &r.Priority, &enabled, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("controlplane: get route: %w", err)
	}
	r.Config = json.RawMessage(cfgStr)
	r.Enabled = enabled == 1
	return &r, nil
}

// UpsertRoute inserts or replaces a route.
func (cp *ControlPlane) UpsertRoute(ctx context.Context, r Route) error {
	enabled := 0
	if r.Enabled {
		enabled = 1
	}
	if r.Config == nil {
		r.Config = json.RawMessage(`{}`)
	}
	_, err := cp.db.ExecContext(ctx,
		`INSERT INTO hpx_routes (service_name, strategy, endpoint, config, priority, enabled)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(service_name) DO UPDATE SET
		     strategy = excluded.strategy,
		     endpoint = excluded.endpoint,
		     config = excluded.config,
		     priority = excluded.priority,
		     enabled = excluded.enabled`,
		r.ServiceName, r.Strategy, r.Endpoint, string(r.Config), r.Priority, enabled)
	if err != nil {
		return fmt.Errorf("controlplane: upsert route: %w", err)
	}
	return nil
}

// DeleteRoute removes a route by service name.
func (cp *ControlPlane) DeleteRoute(ctx context.Context, serviceName string) error {
	_, err := cp.db.ExecContext(ctx,
		`DELETE FROM hpx_routes WHERE service_name = ?`, serviceName)
	return err
}

// SetRouteStrategy changes only the strategy of an existing route.
func (cp *ControlPlane) SetRouteStrategy(ctx context.Context, serviceName, strategy string) error {
	_, err := cp.db.ExecContext(ctx,
		`UPDATE hpx_routes SET strategy = ? WHERE service_name = ?`,
		strategy, serviceName)
	return err
}

// ListRoutes returns an iterator over all enabled routes.
func (cp *ControlPlane) ListRoutes(ctx context.Context) iter.Seq2[Route, error] {
	return func(yield func(Route, error) bool) {
		rows, err := cp.db.QueryContext(ctx,
			`SELECT service_name, strategy, COALESCE(endpoint,''), COALESCE(config,'{}'),
			        priority, enabled, updated_at
			 FROM hpx_routes WHERE enabled = 1 ORDER BY priority DESC, service_name`)
		if err != nil {
			yield(Route{}, fmt.Errorf("controlplane: list routes: %w", err))
			return
		}
		defer rows.Close()

		for rows.Next() {
			var r Route
			var cfgStr string
			var enabled int
			if err := rows.Scan(&r.ServiceName, &r.Strategy, &r.Endpoint, &cfgStr,
				&r.Priority, &enabled, &r.UpdatedAt); err != nil {
				yield(Route{}, fmt.Errorf("controlplane: scan route: %w", err))
				return
			}
			r.Config = json.RawMessage(cfgStr)
			r.Enabled = enabled == 1
			if !yield(r, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(Route{}, err)
		}
	}
}

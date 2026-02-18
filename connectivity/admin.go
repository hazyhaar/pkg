package connectivity

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// Admin provides CRUD operations on the routes table, suitable for
// exposure as MCP tools so an LLM can administer routes at runtime.
//
// All mutations go through SQLite, so the Watch loop automatically
// picks up changes — no need to call Reload manually.
type Admin struct {
	db *sql.DB
}

// NewAdmin creates an Admin backed by the given routes database.
// The database must have the routes schema applied (via Init).
func NewAdmin(db *sql.DB) *Admin {
	return &Admin{db: db}
}

// RouteRow represents a single row from the routes table.
type RouteRow struct {
	ServiceName string          `json:"service_name"`
	Strategy    string          `json:"strategy"`
	Endpoint    string          `json:"endpoint,omitempty"`
	Config      json.RawMessage `json:"config,omitempty"`
	UpdatedAt   int64           `json:"updated_at"`
}

// ListRoutes returns all routes from the SQLite table.
func (a *Admin) ListRoutes(ctx context.Context) ([]RouteRow, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT service_name, strategy, COALESCE(endpoint, ''), COALESCE(config, '{}'), updated_at FROM routes ORDER BY service_name`)
	if err != nil {
		return nil, fmt.Errorf("admin: list routes: %w", err)
	}
	defer rows.Close()

	var result []RouteRow
	for rows.Next() {
		var r RouteRow
		var cfgStr string
		if err := rows.Scan(&r.ServiceName, &r.Strategy, &r.Endpoint, &cfgStr, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("admin: scan route: %w", err)
		}
		r.Config = json.RawMessage(cfgStr)
		result = append(result, r)
	}
	return result, rows.Err()
}

// GetRoute returns a single route by service name.
func (a *Admin) GetRoute(ctx context.Context, serviceName string) (*RouteRow, error) {
	var r RouteRow
	var cfgStr string
	err := a.db.QueryRowContext(ctx,
		`SELECT service_name, strategy, COALESCE(endpoint, ''), COALESCE(config, '{}'), updated_at FROM routes WHERE service_name = ?`,
		serviceName).Scan(&r.ServiceName, &r.Strategy, &r.Endpoint, &cfgStr, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("admin: get route: %w", err)
	}
	r.Config = json.RawMessage(cfgStr)
	return &r, nil
}

// UpsertRoute inserts or updates a route in the routes table.
// On conflict (same service_name), strategy, endpoint, and config are updated;
// updated_at is refreshed by the trigger.
// The watcher will detect the change and trigger a Reload automatically.
func (a *Admin) UpsertRoute(ctx context.Context, serviceName, strategy, endpoint string, config json.RawMessage) error {
	if config == nil {
		config = json.RawMessage(`{}`)
	}
	_, err := a.db.ExecContext(ctx,
		`INSERT INTO routes (service_name, strategy, endpoint, config)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(service_name) DO UPDATE SET
		     strategy = excluded.strategy,
		     endpoint = excluded.endpoint,
		     config   = excluded.config`,
		serviceName, strategy, endpoint, string(config))
	if err != nil {
		return fmt.Errorf("admin: upsert route: %w", err)
	}
	return nil
}

// DeleteRoute removes a route from the routes table.
// The watcher will detect the change and close any associated handler.
func (a *Admin) DeleteRoute(ctx context.Context, serviceName string) error {
	result, err := a.db.ExecContext(ctx,
		`DELETE FROM routes WHERE service_name = ?`, serviceName)
	if err != nil {
		return fmt.Errorf("admin: delete route: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("admin: route %q not found", serviceName)
	}
	return nil
}

// SetStrategy changes only the strategy of an existing route.
// Useful for quick enable/disable: set to "noop" to disable, "local" to
// re-enable with zero downtime.
func (a *Admin) SetStrategy(ctx context.Context, serviceName, strategy string) error {
	result, err := a.db.ExecContext(ctx,
		`UPDATE routes SET strategy = ? WHERE service_name = ?`,
		strategy, serviceName)
	if err != nil {
		return fmt.Errorf("admin: set strategy: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("admin: route %q not found", serviceName)
	}
	return nil
}

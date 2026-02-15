package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
)

// MiddlewareEntry represents a row from the hpx_middleware table.
type MiddlewareEntry struct {
	ID          int64           `json:"id"`
	ServiceName string          `json:"service_name"`
	Middleware  string          `json:"middleware"`
	Position    int             `json:"position"`
	Config      json.RawMessage `json:"config,omitempty"`
	Enabled     bool            `json:"enabled"`
}

// AddMiddleware appends a middleware to a service's chain.
func (cp *ControlPlane) AddMiddleware(ctx context.Context, serviceName, middleware string, position int, config json.RawMessage) error {
	if config == nil {
		config = json.RawMessage(`{}`)
	}
	_, err := cp.db.ExecContext(ctx,
		`INSERT INTO hpx_middleware (service_name, middleware, position, config)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(service_name, middleware) DO UPDATE SET
		     position = excluded.position, config = excluded.config`,
		serviceName, middleware, position, string(config))
	if err != nil {
		return fmt.Errorf("controlplane: add middleware: %w", err)
	}
	return nil
}

// RemoveMiddleware removes a middleware from a service's chain.
func (cp *ControlPlane) RemoveMiddleware(ctx context.Context, serviceName, middleware string) error {
	_, err := cp.db.ExecContext(ctx,
		`DELETE FROM hpx_middleware WHERE service_name = ? AND middleware = ?`,
		serviceName, middleware)
	return err
}

// GetMiddlewareChain returns the ordered middleware chain for a service.
func (cp *ControlPlane) GetMiddlewareChain(ctx context.Context, serviceName string) ([]MiddlewareEntry, error) {
	rows, err := cp.db.QueryContext(ctx,
		`SELECT id, service_name, middleware, position, COALESCE(config,'{}'), enabled
		 FROM hpx_middleware
		 WHERE service_name = ? AND enabled = 1
		 ORDER BY position`, serviceName)
	if err != nil {
		return nil, fmt.Errorf("controlplane: get middleware chain: %w", err)
	}
	defer rows.Close()

	var chain []MiddlewareEntry
	for rows.Next() {
		var m MiddlewareEntry
		var cfgStr string
		var enabled int
		if err := rows.Scan(&m.ID, &m.ServiceName, &m.Middleware, &m.Position, &cfgStr, &enabled); err != nil {
			return nil, fmt.Errorf("controlplane: scan middleware: %w", err)
		}
		m.Config = json.RawMessage(cfgStr)
		m.Enabled = enabled == 1
		chain = append(chain, m)
	}
	return chain, rows.Err()
}

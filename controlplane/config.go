package controlplane

import (
	"context"
	"database/sql"
	"fmt"
	"iter"
)

// ConfigEntry represents a key-value pair from the hpx_config table.
type ConfigEntry struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	UpdatedAt   int64  `json:"updated_at"`
}

// GetConfig returns the value for a configuration key.
// Returns "" and no error if the key does not exist.
func (cp *ControlPlane) GetConfig(ctx context.Context, key string) (string, error) {
	var value string
	err := cp.db.QueryRowContext(ctx,
		`SELECT value FROM hpx_config WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("controlplane: get config %q: %w", key, err)
	}
	return value, nil
}

// SetConfig inserts or updates a configuration key-value pair.
func (cp *ControlPlane) SetConfig(ctx context.Context, key, value, description string) error {
	_, err := cp.db.ExecContext(ctx,
		`INSERT INTO hpx_config (key, value, description) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, description = excluded.description`,
		key, value, description)
	if err != nil {
		return fmt.Errorf("controlplane: set config %q: %w", key, err)
	}
	return nil
}

// DeleteConfig removes a configuration key.
func (cp *ControlPlane) DeleteConfig(ctx context.Context, key string) error {
	_, err := cp.db.ExecContext(ctx,
		`DELETE FROM hpx_config WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("controlplane: delete config %q: %w", key, err)
	}
	return nil
}

// ListConfig returns an iterator over all configuration entries.
func (cp *ControlPlane) ListConfig(ctx context.Context) iter.Seq2[ConfigEntry, error] {
	return func(yield func(ConfigEntry, error) bool) {
		rows, err := cp.db.QueryContext(ctx,
			`SELECT key, value, COALESCE(description, ''), updated_at FROM hpx_config ORDER BY key`)
		if err != nil {
			yield(ConfigEntry{}, fmt.Errorf("controlplane: list config: %w", err))
			return
		}
		defer rows.Close()

		for rows.Next() {
			var e ConfigEntry
			if err := rows.Scan(&e.Key, &e.Value, &e.Description, &e.UpdatedAt); err != nil {
				yield(ConfigEntry{}, fmt.Errorf("controlplane: scan config: %w", err))
				return
			}
			if !yield(e, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(ConfigEntry{}, err)
		}
	}
}

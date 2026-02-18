package channels

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// Admin provides CRUD operations on the channels table, suitable for
// exposure as MCP tools so an LLM can administer channels at runtime.
//
// All mutations go through SQLite, so the Watch loop automatically
// picks up changes — no need to call Reload manually.
type Admin struct {
	db *sql.DB
}

// NewAdmin creates an Admin backed by the given database.
// The database must have the channels schema applied (via Init).
func NewAdmin(db *sql.DB) *Admin {
	return &Admin{db: db}
}

// ChannelRow represents a single row from the channels table.
type ChannelRow struct {
	Name      string          `json:"name"`
	Platform  string          `json:"platform"`
	Enabled   bool            `json:"enabled"`
	Config    json.RawMessage `json:"config,omitempty"`
	AuthState json.RawMessage `json:"auth_state,omitempty"`
	UpdatedAt int64           `json:"updated_at"`
}

// ListChannels returns all channels from the SQLite table.
func (a *Admin) ListChannels(ctx context.Context) ([]ChannelRow, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT name, platform, enabled, COALESCE(config, '{}'), COALESCE(auth_state, '{}'), updated_at FROM channels ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("admin: list channels: %w", err)
	}
	defer rows.Close()

	var result []ChannelRow
	for rows.Next() {
		var r ChannelRow
		var cfgStr, authStr string
		var enabled int
		if err := rows.Scan(&r.Name, &r.Platform, &enabled, &cfgStr, &authStr, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("admin: scan channel: %w", err)
		}
		r.Enabled = enabled == 1
		r.Config = json.RawMessage(cfgStr)
		r.AuthState = json.RawMessage(authStr)
		result = append(result, r)
	}
	return result, rows.Err()
}

// GetChannel returns a single channel by name.
func (a *Admin) GetChannel(ctx context.Context, name string) (*ChannelRow, error) {
	var r ChannelRow
	var cfgStr, authStr string
	var enabled int
	err := a.db.QueryRowContext(ctx,
		`SELECT name, platform, enabled, COALESCE(config, '{}'), COALESCE(auth_state, '{}'), updated_at FROM channels WHERE name = ?`,
		name).Scan(&r.Name, &r.Platform, &enabled, &cfgStr, &authStr, &r.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("admin: get channel: %w", err)
	}
	r.Enabled = enabled == 1
	r.Config = json.RawMessage(cfgStr)
	r.AuthState = json.RawMessage(authStr)
	return &r, nil
}

// UpsertChannel inserts or updates a channel in the channels table.
// On conflict (same name), only platform, enabled, and config are updated;
// auth_state is preserved so that active sessions (e.g. WhatsApp pairing)
// are not lost when an admin changes the channel config.
// The watcher will detect the change and trigger a Reload automatically.
func (a *Admin) UpsertChannel(ctx context.Context, name, platform string, enabled bool, config json.RawMessage) error {
	if config == nil {
		config = json.RawMessage(`{}`)
	}
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	_, err := a.db.ExecContext(ctx,
		`INSERT INTO channels (name, platform, enabled, config)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		     platform = excluded.platform,
		     enabled  = excluded.enabled,
		     config   = excluded.config`,
		name, platform, enabledInt, string(config))
	if err != nil {
		return fmt.Errorf("admin: upsert channel: %w", err)
	}
	return nil
}

// DeleteChannel removes a channel from the channels table.
// The watcher will detect the change and close the associated connection.
func (a *Admin) DeleteChannel(ctx context.Context, name string) error {
	result, err := a.db.ExecContext(ctx,
		`DELETE FROM channels WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("admin: delete channel: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("admin: channel %q not found", name)
	}
	return nil
}

// SetEnabled enables or disables a channel without deleting its config.
// Set enabled=false to shut down the connection; set enabled=true to restart.
// Analogous to connectivity's noop strategy toggle.
func (a *Admin) SetEnabled(ctx context.Context, name string, enabled bool) error {
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	result, err := a.db.ExecContext(ctx,
		`UPDATE channels SET enabled = ? WHERE name = ?`,
		enabledInt, name)
	if err != nil {
		return fmt.Errorf("admin: set enabled: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("admin: channel %q not found", name)
	}
	return nil
}

// UpdateAuthState updates the persistent auth state for a channel.
// This is called by channel implementations when authentication state
// changes (e.g., WhatsApp pairing completes, token refresh).
func (a *Admin) UpdateAuthState(ctx context.Context, name string, authState json.RawMessage) error {
	if authState == nil {
		authState = json.RawMessage(`{}`)
	}
	result, err := a.db.ExecContext(ctx,
		`UPDATE channels SET auth_state = ? WHERE name = ?`,
		string(authState), name)
	if err != nil {
		return fmt.Errorf("admin: update auth state: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("admin: channel %q not found", name)
	}
	return nil
}

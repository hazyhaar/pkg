package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"iter"
	"time"
)

// ServiceEntry represents a row from the hpx_services table.
type ServiceEntry struct {
	ServiceName  string          `json:"service_name"`
	Version      string          `json:"version,omitempty"`
	Host         string          `json:"host,omitempty"`
	Port         int             `json:"port,omitempty"`
	Protocol     string          `json:"protocol"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	HealthURL    string          `json:"health_url,omitempty"`
	Status       string          `json:"status"`
	LastSeen     int64           `json:"last_seen,omitempty"`
	RegisteredAt int64           `json:"registered_at"`
}

// RegisterService registers or updates a service in the discovery registry.
func (cp *ControlPlane) RegisterService(ctx context.Context, s ServiceEntry) error {
	if s.Metadata == nil {
		s.Metadata = json.RawMessage(`{}`)
	}
	if s.Protocol == "" {
		s.Protocol = "http"
	}
	if s.Status == "" {
		s.Status = "active"
	}
	_, err := cp.db.ExecContext(ctx,
		`INSERT INTO hpx_services (service_name, version, host, port, protocol, metadata, health_url, status, last_seen)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(service_name) DO UPDATE SET
		     version = excluded.version,
		     host = excluded.host,
		     port = excluded.port,
		     protocol = excluded.protocol,
		     metadata = excluded.metadata,
		     health_url = excluded.health_url,
		     status = excluded.status,
		     last_seen = excluded.last_seen`,
		s.ServiceName, s.Version, s.Host, s.Port, s.Protocol,
		string(s.Metadata), s.HealthURL, s.Status, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("controlplane: register service: %w", err)
	}
	return nil
}

// DeregisterService removes a service from the registry.
func (cp *ControlPlane) DeregisterService(ctx context.Context, serviceName string) error {
	_, err := cp.db.ExecContext(ctx,
		`DELETE FROM hpx_services WHERE service_name = ?`, serviceName)
	return err
}

// GetService returns a single service entry, or nil if not found.
func (cp *ControlPlane) GetService(ctx context.Context, serviceName string) (*ServiceEntry, error) {
	var s ServiceEntry
	var metaStr string
	var lastSeen sql.NullInt64
	err := cp.db.QueryRowContext(ctx,
		`SELECT service_name, COALESCE(version,''), COALESCE(host,''), COALESCE(port,0),
		        protocol, COALESCE(metadata,'{}'), COALESCE(health_url,''), status, last_seen, registered_at
		 FROM hpx_services WHERE service_name = ?`, serviceName).
		Scan(&s.ServiceName, &s.Version, &s.Host, &s.Port, &s.Protocol,
			&metaStr, &s.HealthURL, &s.Status, &lastSeen, &s.RegisteredAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("controlplane: get service: %w", err)
	}
	s.Metadata = json.RawMessage(metaStr)
	if lastSeen.Valid {
		s.LastSeen = lastSeen.Int64
	}
	return &s, nil
}

// ListServices returns an iterator over all active services.
func (cp *ControlPlane) ListServices(ctx context.Context) iter.Seq2[ServiceEntry, error] {
	return func(yield func(ServiceEntry, error) bool) {
		rows, err := cp.db.QueryContext(ctx,
			`SELECT service_name, COALESCE(version,''), COALESCE(host,''), COALESCE(port,0),
			        protocol, COALESCE(metadata,'{}'), COALESCE(health_url,''), status, last_seen, registered_at
			 FROM hpx_services WHERE status = 'active' ORDER BY service_name`)
		if err != nil {
			yield(ServiceEntry{}, fmt.Errorf("controlplane: list services: %w", err))
			return
		}
		defer rows.Close()

		for rows.Next() {
			var s ServiceEntry
			var metaStr string
			var lastSeen sql.NullInt64
			if err := rows.Scan(&s.ServiceName, &s.Version, &s.Host, &s.Port, &s.Protocol,
				&metaStr, &s.HealthURL, &s.Status, &lastSeen, &s.RegisteredAt); err != nil {
				yield(ServiceEntry{}, fmt.Errorf("controlplane: scan service: %w", err))
				return
			}
			s.Metadata = json.RawMessage(metaStr)
			if lastSeen.Valid {
				s.LastSeen = lastSeen.Int64
			}
			if !yield(s, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(ServiceEntry{}, err)
		}
	}
}

// Heartbeat updates the last_seen timestamp of a service.
func (cp *ControlPlane) Heartbeat(ctx context.Context, serviceName string) error {
	_, err := cp.db.ExecContext(ctx,
		`UPDATE hpx_services SET last_seen = ? WHERE service_name = ?`,
		time.Now().Unix(), serviceName)
	return err
}

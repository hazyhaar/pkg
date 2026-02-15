package controlplane

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/hazyhaar/pkg/idgen"
)

// CertEntry represents a row from the hpx_certs table.
type CertEntry struct {
	CertID    string `json:"cert_id"`
	Domain    string `json:"domain"`
	CertPEM   string `json:"cert_pem"`
	KeyPEM    string `json:"key_pem"`
	CaPEM     string `json:"ca_pem,omitempty"`
	NotBefore int64  `json:"not_before,omitempty"`
	NotAfter  int64  `json:"not_after,omitempty"`
	IsDefault bool   `json:"is_default"`
	CreatedAt int64  `json:"created_at"`
}

// StoreCert inserts or replaces a TLS certificate.
func (cp *ControlPlane) StoreCert(ctx context.Context, c CertEntry) error {
	if c.CertID == "" {
		c.CertID = idgen.Prefixed("cert_", idgen.Default)()
	}
	isDefault := 0
	if c.IsDefault {
		isDefault = 1
	}
	_, err := cp.db.ExecContext(ctx,
		`INSERT INTO hpx_certs (cert_id, domain, cert_pem, key_pem, ca_pem, not_before, not_after, is_default)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(cert_id) DO UPDATE SET
		     domain = excluded.domain,
		     cert_pem = excluded.cert_pem,
		     key_pem = excluded.key_pem,
		     ca_pem = excluded.ca_pem,
		     not_before = excluded.not_before,
		     not_after = excluded.not_after,
		     is_default = excluded.is_default`,
		c.CertID, c.Domain, c.CertPEM, c.KeyPEM, c.CaPEM,
		c.NotBefore, c.NotAfter, isDefault)
	if err != nil {
		return fmt.Errorf("controlplane: store cert: %w", err)
	}
	return nil
}

// GetCertForDomain returns the certificate for a domain, or nil.
func (cp *ControlPlane) GetCertForDomain(ctx context.Context, domain string) (*CertEntry, error) {
	var c CertEntry
	var isDefault int
	var caPEM sql.NullString
	err := cp.db.QueryRowContext(ctx,
		`SELECT cert_id, domain, cert_pem, key_pem, ca_pem, COALESCE(not_before,0),
		        COALESCE(not_after,0), is_default, created_at
		 FROM hpx_certs WHERE domain = ? ORDER BY created_at DESC LIMIT 1`, domain).
		Scan(&c.CertID, &c.Domain, &c.CertPEM, &c.KeyPEM, &caPEM,
			&c.NotBefore, &c.NotAfter, &isDefault, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("controlplane: get cert: %w", err)
	}
	if caPEM.Valid {
		c.CaPEM = caPEM.String
	}
	c.IsDefault = isDefault == 1
	return &c, nil
}

// GetDefaultCert returns the default certificate (is_default=1).
func (cp *ControlPlane) GetDefaultCert(ctx context.Context) (*CertEntry, error) {
	var c CertEntry
	var isDefault int
	var caPEM sql.NullString
	err := cp.db.QueryRowContext(ctx,
		`SELECT cert_id, domain, cert_pem, key_pem, ca_pem, COALESCE(not_before,0),
		        COALESCE(not_after,0), is_default, created_at
		 FROM hpx_certs WHERE is_default = 1 LIMIT 1`).
		Scan(&c.CertID, &c.Domain, &c.CertPEM, &c.KeyPEM, &caPEM,
			&c.NotBefore, &c.NotAfter, &isDefault, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("controlplane: get default cert: %w", err)
	}
	if caPEM.Valid {
		c.CaPEM = caPEM.String
	}
	c.IsDefault = true
	return &c, nil
}

// DeleteCert removes a certificate by ID.
func (cp *ControlPlane) DeleteCert(ctx context.Context, certID string) error {
	_, err := cp.db.ExecContext(ctx,
		`DELETE FROM hpx_certs WHERE cert_id = ?`, certID)
	return err
}

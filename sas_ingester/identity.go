package sas_ingester

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// JWTClaims holds the decoded JWT payload relevant to identity.
type JWTClaims struct {
	Sub       string `json:"sub"`
	DossierID string `json:"dossier_id,omitempty"`
	Exp       int64  `json:"exp,omitempty"`
	Iat       int64  `json:"iat,omitempty"`
}

// ParseJWT decodes a HS256 JWT token and validates the signature + expiry.
// Returns the claims on success.
func ParseJWT(tokenStr, secret string) (*JWTClaims, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid jwt: expected 3 parts, got %d", len(parts))
	}

	// Verify signature (HS256).
	signingInput := parts[0] + "." + parts[1]
	expectedSig, err := base64URLDecode(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	actualSig := mac.Sum(nil)

	if !hmac.Equal(actualSig, expectedSig) {
		return nil, fmt.Errorf("invalid jwt signature")
	}

	// Decode header — verify alg.
	headerJSON, err := base64URLDecode(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}
	if header.Alg != "HS256" {
		return nil, fmt.Errorf("unsupported alg: %s", header.Alg)
	}

	// Decode payload.
	payloadJSON, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	var claims JWTClaims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, fmt.Errorf("parse payload: %w", err)
	}

	// Validate expiry.
	if claims.Exp > 0 && time.Now().Unix() > claims.Exp {
		return nil, fmt.Errorf("jwt expired")
	}

	if claims.Sub == "" {
		return nil, fmt.Errorf("jwt missing sub claim")
	}

	return &claims, nil
}

// ExtractDossierID resolves the dossier_id from the JWT claim.
// Returns an empty string if the JWT does not carry a dossier_id — the caller
// must then generate an opaque server-side ID (never derive from Sub).
func ExtractDossierID(claims *JWTClaims) string {
	return claims.DossierID
}

func base64URLDecode(s string) ([]byte, error) {
	// Pad if needed.
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

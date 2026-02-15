package sas_ingester

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func makeTestJWT(t *testing.T, claims map[string]interface{}, secret string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, _ := json.Marshal(claims)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	sigInput := header + "." + payloadB64

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(sigInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return sigInput + "." + sig
}

func TestParseJWT_Valid(t *testing.T) {
	secret := "test-secret-key"
	token := makeTestJWT(t, map[string]interface{}{
		"sub":        "user-123",
		"dossier_id": "d-456",
		"exp":        time.Now().Add(time.Hour).Unix(),
	}, secret)

	claims, err := ParseJWT(token, secret)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Sub != "user-123" {
		t.Errorf("sub = %q, want user-123", claims.Sub)
	}
	if claims.DossierID != "d-456" {
		t.Errorf("dossier_id = %q, want d-456", claims.DossierID)
	}
}

func TestParseJWT_Expired(t *testing.T) {
	secret := "test-secret-key"
	token := makeTestJWT(t, map[string]interface{}{
		"sub": "user-123",
		"exp": time.Now().Add(-time.Hour).Unix(),
	}, secret)

	_, err := ParseJWT(token, secret)
	if err == nil {
		t.Fatal("expected expiry error")
	}
}

func TestParseJWT_BadSignature(t *testing.T) {
	token := makeTestJWT(t, map[string]interface{}{"sub": "user-123"}, "secret-1")
	_, err := ParseJWT(token, "secret-2")
	if err == nil {
		t.Fatal("expected signature error")
	}
}

func TestExtractDossierID(t *testing.T) {
	// With explicit dossier_id.
	c1 := &JWTClaims{Sub: "user-1", DossierID: "d-explicit"}
	if got := ExtractDossierID(c1); got != "d-explicit" {
		t.Errorf("got %q, want d-explicit", got)
	}

	// No dossier_id → returns empty string (caller must generate opaque ID).
	c2 := &JWTClaims{Sub: "user-1"}
	if got := ExtractDossierID(c2); got != "" {
		t.Errorf("got %q, want empty string (never derive from sub)", got)
	}
}

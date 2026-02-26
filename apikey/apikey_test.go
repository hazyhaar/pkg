package apikey

import (
	"os"
	"strings"
	"testing"
	"time"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	f, err := os.CreateTemp("", "apikey_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	t.Cleanup(func() { os.Remove(path) })

	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestGenerate(t *testing.T) {
	s := tempStore(t)

	clearKey, key, err := s.Generate("key_001", "user_abc", "Mon LLM", []string{"sas_ingester", "veille"}, 60)
	if err != nil {
		t.Fatal(err)
	}

	// Clear key format.
	if !strings.HasPrefix(clearKey, Prefix) {
		t.Errorf("clearKey should start with %q, got %q", Prefix, clearKey[:4])
	}
	if len(clearKey) != 67 { // "hk_" (3) + 64 hex chars
		t.Errorf("clearKey length = %d, want 67", len(clearKey))
	}

	// Key record.
	if key.OwnerID != "user_abc" {
		t.Errorf("OwnerID = %q", key.OwnerID)
	}
	if key.Name != "Mon LLM" {
		t.Errorf("Name = %q", key.Name)
	}
	if len(key.Services) != 2 {
		t.Errorf("Services = %v", key.Services)
	}
	if key.RateLimit != 60 {
		t.Errorf("RateLimit = %d", key.RateLimit)
	}
	if key.Prefix != clearKey[:8] {
		t.Errorf("Prefix = %q, want %q", key.Prefix, clearKey[:8])
	}
}

func TestResolve(t *testing.T) {
	s := tempStore(t)

	clearKey, _, err := s.Generate("key_002", "user_xyz", "Test Key", []string{"sas_ingester"}, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Resolve with valid key.
	resolved, err := s.Resolve(clearKey)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.OwnerID != "user_xyz" {
		t.Errorf("OwnerID = %q, want user_xyz", resolved.OwnerID)
	}
	if !resolved.HasService("sas_ingester") {
		t.Error("should have sas_ingester service")
	}
	if resolved.HasService("horum") {
		t.Error("should not have horum service")
	}
}

func TestResolve_InvalidFormat(t *testing.T) {
	s := tempStore(t)

	_, err := s.Resolve("not_a_valid_key")
	if err == nil {
		t.Error("expected error for invalid format")
	}
	if !strings.Contains(err.Error(), "invalid key format") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolve_UnknownKey(t *testing.T) {
	s := tempStore(t)

	_, err := s.Resolve("hk_0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Error("expected error for unknown key")
	}
	if !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRevoke(t *testing.T) {
	s := tempStore(t)

	clearKey, _, err := s.Generate("key_003", "user_r", "Revoke Me", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Revoke.
	if err := s.Revoke("key_003"); err != nil {
		t.Fatal(err)
	}

	// Resolve should fail.
	_, err = s.Resolve(clearKey)
	if err == nil {
		t.Error("expected error for revoked key")
	}
	if !strings.Contains(err.Error(), "revoked") {
		t.Errorf("unexpected error: %v", err)
	}

	// Double revoke should fail.
	err = s.Revoke("key_003")
	if err == nil {
		t.Error("expected error for double revoke")
	}
}

func TestExpiry(t *testing.T) {
	s := tempStore(t)

	clearKey, _, err := s.Generate("key_004", "user_e", "Expires", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Set expiry to the past.
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if err := s.SetExpiry("key_004", past); err != nil {
		t.Fatal(err)
	}

	// Resolve should fail.
	_, err = s.Resolve(clearKey)
	if err == nil {
		t.Error("expected error for expired key")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("unexpected error: %v", err)
	}

	// Set expiry to the future — should work again.
	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	if err := s.SetExpiry("key_004", future); err != nil {
		t.Fatal(err)
	}
	_, err = s.Resolve(clearKey)
	if err != nil {
		t.Fatalf("expected success after extending expiry: %v", err)
	}
}

func TestList(t *testing.T) {
	s := tempStore(t)

	s.Generate("key_L1", "owner_L", "Key 1", []string{"a"}, 10)
	s.Generate("key_L2", "owner_L", "Key 2", []string{"b"}, 20)
	s.Generate("key_L3", "other_owner", "Key 3", nil, 0)

	keys, err := s.List("owner_L")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("got %d keys, want 2", len(keys))
	}
	// Most recent first.
	if keys[0].Name != "Key 2" {
		t.Errorf("keys[0].Name = %q, want Key 2 (most recent)", keys[0].Name)
	}
}

func TestUpdateServices(t *testing.T) {
	s := tempStore(t)

	clearKey, _, _ := s.Generate("key_U1", "owner_U", "Updatable", []string{"a"}, 0)

	// Update services.
	if err := s.UpdateServices("key_U1", []string{"a", "b", "c"}); err != nil {
		t.Fatal(err)
	}

	resolved, _ := s.Resolve(clearKey)
	if len(resolved.Services) != 3 {
		t.Errorf("Services after update = %v", resolved.Services)
	}
	if !resolved.HasService("c") {
		t.Error("should have service c after update")
	}
}

func TestHasService_EmptyMeansAll(t *testing.T) {
	k := &Key{Services: nil}
	if !k.HasService("anything") {
		t.Error("empty services should allow all")
	}
}

func TestHashKey_Deterministic(t *testing.T) {
	h1 := hashKey("hk_test123")
	h2 := hashKey("hk_test123")
	if h1 != h2 {
		t.Error("hashKey should be deterministic")
	}

	h3 := hashKey("hk_different")
	if h1 == h3 {
		t.Error("different keys should have different hashes")
	}
}

func TestParseServices(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"[]", 0},
		{"", 0},
		{`["a"]`, 1},
		{`["a","b","c"]`, 3},
	}
	for _, tt := range tests {
		got := parseServices(tt.input)
		if len(got) != tt.want {
			t.Errorf("parseServices(%q) = %v (len %d), want len %d", tt.input, got, len(got), tt.want)
		}
	}
}

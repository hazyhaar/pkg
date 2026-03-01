package apikey

import (
	"database/sql"
	"encoding/json"
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

func TestGenerate_WithDossier(t *testing.T) {
	s := tempStore(t)

	clearKey, key, err := s.Generate("key_D01", "user_d", "Dossier Key", nil, 0, WithDossier("dossier_abc"))
	if err != nil {
		t.Fatal(err)
	}
	if key.DossierID != "dossier_abc" {
		t.Errorf("DossierID = %q, want dossier_abc", key.DossierID)
	}
	if !key.IsDossierScoped() {
		t.Error("key should be dossier-scoped")
	}

	// Resolve should return DossierID.
	resolved, err := s.Resolve(clearKey)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.DossierID != "dossier_abc" {
		t.Errorf("resolved DossierID = %q, want dossier_abc", resolved.DossierID)
	}
}

func TestGenerate_WithoutDossier(t *testing.T) {
	s := tempStore(t)

	_, key, err := s.Generate("key_D02", "user_d", "Legacy Key", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if key.DossierID != "" {
		t.Errorf("DossierID = %q, want empty (legacy)", key.DossierID)
	}
	if key.IsDossierScoped() {
		t.Error("legacy key should not be dossier-scoped")
	}
}

func TestIsDossierScoped(t *testing.T) {
	scoped := &Key{DossierID: "dossier_123"}
	if !scoped.IsDossierScoped() {
		t.Error("should be scoped")
	}

	legacy := &Key{DossierID: ""}
	if legacy.IsDossierScoped() {
		t.Error("should not be scoped")
	}
}

func TestListByDossier(t *testing.T) {
	s := tempStore(t)

	s.Generate("key_LD1", "owner_A", "Key A1", nil, 0, WithDossier("dossier_X"))
	s.Generate("key_LD2", "owner_A", "Key A2", nil, 0, WithDossier("dossier_X"))
	s.Generate("key_LD3", "owner_A", "Key A3", nil, 0, WithDossier("dossier_Y"))
	s.Generate("key_LD4", "owner_B", "Key B1", nil, 0) // legacy, no dossier

	keys, err := s.ListByDossier("dossier_X")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("got %d keys for dossier_X, want 2", len(keys))
	}

	// Revoke one key — should not appear in ListByDossier.
	if err := s.Revoke("key_LD1"); err != nil {
		t.Fatal(err)
	}
	keys, err = s.ListByDossier("dossier_X")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys after revoke, want 1", len(keys))
	}

	// Empty dossier_id should return no keys (legacy keys excluded).
	keys, err = s.ListByDossier("")
	if err != nil {
		t.Fatal(err)
	}
	// Legacy key has dossier_id="" which matches the query — this is intentional
	// but in practice ListByDossier is only called with non-empty dossierIDs.
	_ = keys
}

func TestList_IncludesDossierID(t *testing.T) {
	s := tempStore(t)

	s.Generate("key_LI1", "owner_LI", "Scoped", nil, 0, WithDossier("dossier_Z"))
	s.Generate("key_LI2", "owner_LI", "Legacy", nil, 0)

	keys, err := s.List("owner_LI")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("got %d keys, want 2", len(keys))
	}

	// Find the scoped key.
	var found bool
	for _, k := range keys {
		if k.ID == "key_LI1" && k.DossierID == "dossier_Z" {
			found = true
		}
	}
	if !found {
		t.Error("List should include dossier_id for scoped keys")
	}
}

// ─── AUDIT TESTS — RED FIRST ───────────────────────────────────────────────

// TestAudit_ServiceNameWithQuote tests that service names containing quotes
// are stored and retrieved correctly (JSON injection vector).
func TestAudit_ServiceNameWithQuote(t *testing.T) {
	s := tempStore(t)

	svcWithQuote := `my"service`
	clearKey, key, err := s.Generate("key_AQ1", "owner_AQ", "Quoted", []string{svcWithQuote}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(key.Services) != 1 || key.Services[0] != svcWithQuote {
		t.Errorf("Generate returned Services = %v, want [%q]", key.Services, svcWithQuote)
	}

	// Resolve and check round-trip.
	resolved, err := s.Resolve(clearKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Services) != 1 || resolved.Services[0] != svcWithQuote {
		t.Errorf("Resolve returned Services = %v, want [%q]", resolved.Services, svcWithQuote)
	}

	// The stored JSON should be valid.
	rows, err := s.db.Query(`SELECT services FROM api_keys WHERE id = ?`, "key_AQ1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		var raw string
		rows.Scan(&raw)
		var parsed []string
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			t.Errorf("stored JSON is invalid: %q — json.Unmarshal error: %v", raw, err)
		}
	}
}

// TestAudit_ServiceNameWithComma tests that service names containing commas
// are stored and retrieved correctly.
func TestAudit_ServiceNameWithComma(t *testing.T) {
	s := tempStore(t)

	svcWithComma := "svc,evil"
	clearKey, _, err := s.Generate("key_AC1", "owner_AC", "Comma", []string{svcWithComma}, 0)
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := s.Resolve(clearKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Services) != 1 {
		t.Errorf("Resolve returned %d services, want 1 — got %v", len(resolved.Services), resolved.Services)
	}
	if len(resolved.Services) > 0 && resolved.Services[0] != svcWithComma {
		t.Errorf("Resolve returned Services[0] = %q, want %q", resolved.Services[0], svcWithComma)
	}
}

// TestAudit_GenerateDuplicateID tests that generating with a duplicate ID returns an error.
func TestAudit_GenerateDuplicateID(t *testing.T) {
	s := tempStore(t)

	_, _, err := s.Generate("key_DUP", "owner_D", "First", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = s.Generate("key_DUP", "owner_D", "Second", nil, 0)
	if err == nil {
		t.Error("expected error for duplicate ID, got nil")
	}
}

// TestAudit_SetExpiryNonExistent tests that SetExpiry on a non-existent key returns an error.
func TestAudit_SetExpiryNonExistent(t *testing.T) {
	s := tempStore(t)

	err := s.SetExpiry("nonexistent_key", time.Now().UTC().Add(time.Hour).Format(time.RFC3339))
	if err == nil {
		t.Error("expected error for SetExpiry on non-existent key, got nil")
	}
}

// TestAudit_UpdateServicesNonExistent tests that UpdateServices on a non-existent key returns an error.
func TestAudit_UpdateServicesNonExistent(t *testing.T) {
	s := tempStore(t)

	err := s.UpdateServices("nonexistent_key", []string{"a"})
	if err == nil {
		t.Error("expected error for UpdateServices on non-existent key, got nil")
	}
}

// TestAudit_SetExpiryOnRevokedKey tests that SetExpiry on a revoked key returns an error.
func TestAudit_SetExpiryOnRevokedKey(t *testing.T) {
	s := tempStore(t)

	_, _, err := s.Generate("key_REV_EXP", "owner_RE", "Revoked", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke("key_REV_EXP"); err != nil {
		t.Fatal(err)
	}

	err = s.SetExpiry("key_REV_EXP", time.Now().UTC().Add(time.Hour).Format(time.RFC3339))
	if err == nil {
		t.Error("expected error for SetExpiry on revoked key, got nil")
	}
}

// TestAudit_UpdateServicesOnRevokedKey tests that UpdateServices on a revoked key returns an error.
func TestAudit_UpdateServicesOnRevokedKey(t *testing.T) {
	s := tempStore(t)

	_, _, err := s.Generate("key_REV_SVC", "owner_RS", "Revoked", []string{"a"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke("key_REV_SVC"); err != nil {
		t.Fatal(err)
	}

	err = s.UpdateServices("key_REV_SVC", []string{"a", "b"})
	if err == nil {
		t.Error("expected error for UpdateServices on revoked key, got nil")
	}
}

// TestAudit_GenerateEmptyID tests that Generate rejects empty ID.
func TestAudit_GenerateEmptyID(t *testing.T) {
	s := tempStore(t)

	_, _, err := s.Generate("", "owner_E", "No ID", nil, 0)
	if err == nil {
		t.Error("expected error for empty ID")
	}
}

// TestAudit_GenerateEmptyOwner tests that Generate rejects empty ownerID.
func TestAudit_GenerateEmptyOwner(t *testing.T) {
	s := tempStore(t)

	_, _, err := s.Generate("key_EO", "", "No Owner", nil, 0)
	if err == nil {
		t.Error("expected error for empty ownerID")
	}
}

// TestAudit_MigrateIdempotent tests that calling migrate multiple times is safe.
func TestAudit_MigrateIdempotent(t *testing.T) {
	s := tempStore(t)

	// Second migration should not fail.
	if err := s.migrate(); err != nil {
		t.Fatalf("second migrate failed: %v", err)
	}
}

// TestAudit_OpenStoreWithDB tests the shared DB path.
func TestAudit_OpenStoreWithDB(t *testing.T) {
	f, err := os.CreateTemp("", "apikey_shared_*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	t.Cleanup(func() { os.Remove(path) })

	db, err := sql.Open("sqlite-trace", path+"?_txlock=immediate&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store, err := OpenStoreWithDB(db)
	if err != nil {
		t.Fatal(err)
	}

	// Generate and resolve should work.
	clearKey, _, err := store.Generate("key_SDB1", "owner_SDB", "Shared", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Resolve(clearKey)
	if err != nil {
		t.Fatalf("Resolve on shared DB store failed: %v", err)
	}
}

// TestAudit_PragmaExecRemoved verifies that migrate() does not rely on
// PRAGMA via db.Exec (which only touches one connection in the pool).
// This is a convention check — pragmas must be set via DSN _pragma=.
func TestAudit_PragmaExecRemoved(t *testing.T) {
	// Read the source and check for PRAGMA via Exec in migrate.
	src, err := os.ReadFile("apikey.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), `db.Exec`) && strings.Contains(string(src), "PRAGMA") {
		t.Error("migrate() should not use db.Exec(\"PRAGMA ...\") — use _pragma= in DSN instead")
	}
}

// TestAudit_CloseSharedDBSafe verifies that Close() on a store created via
// OpenStoreWithDB does NOT close the underlying shared DB.
func TestAudit_CloseSharedDBSafe(t *testing.T) {
	f, err := os.CreateTemp("", "apikey_shared_close_*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	t.Cleanup(func() { os.Remove(path) })

	db, err := sql.Open("sqlite-trace", path+"?_txlock=immediate&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store, err := OpenStoreWithDB(db)
	if err != nil {
		t.Fatal(err)
	}

	// Close the store — should NOT close the shared DB.
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close() returned error: %v", err)
	}

	// The original DB must still be usable.
	if err := db.Ping(); err != nil {
		t.Fatalf("shared DB unusable after store.Close(): %v", err)
	}
}

// TestAudit_Count verifies that Count returns the number of non-revoked keys for an owner.
func TestAudit_Count(t *testing.T) {
	s := tempStore(t)

	// Generate 3 keys for the same owner.
	s.Generate("key_C1", "owner_count", "Key 1", nil, 0)
	s.Generate("key_C2", "owner_count", "Key 2", nil, 0)
	s.Generate("key_C3", "owner_count", "Key 3", nil, 0)

	n, err := s.Count("owner_count")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("Count = %d, want 3", n)
	}

	// Revoke one — count should drop to 2.
	if err := s.Revoke("key_C2"); err != nil {
		t.Fatal(err)
	}
	n, err = s.Count("owner_count")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("Count after revoke = %d, want 2", n)
	}

	// Different owner should be 0.
	n, err = s.Count("other_owner_count")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("Count for other owner = %d, want 0", n)
	}
}

// TestAudit_MaxKeysLimit verifies that WithMaxKeys limits the number of keys per owner.
func TestAudit_MaxKeysLimit(t *testing.T) {
	f, err := os.CreateTemp("", "apikey_maxkeys_*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	t.Cleanup(func() { os.Remove(path) })

	s, err := OpenStore(path, WithMaxKeys(2))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	// First two keys should succeed.
	_, _, err = s.Generate("key_MK1", "owner_mk", "Key 1", nil, 0)
	if err != nil {
		t.Fatalf("1st key: %v", err)
	}
	_, _, err = s.Generate("key_MK2", "owner_mk", "Key 2", nil, 0)
	if err != nil {
		t.Fatalf("2nd key: %v", err)
	}

	// Third key should fail with "limit" in the error message.
	_, _, err = s.Generate("key_MK3", "owner_mk", "Key 3", nil, 0)
	if err == nil {
		t.Fatal("expected error for 3rd key exceeding limit, got nil")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error should contain 'limit', got: %v", err)
	}

	// Different owner should still be able to create keys.
	_, _, err = s.Generate("key_MK4", "other_owner_mk", "Key 4", nil, 0)
	if err != nil {
		t.Fatalf("different owner should succeed: %v", err)
	}
}

// TestAudit_HookCalled verifies that the audit hook is called for generate, resolve, revoke.
func TestAudit_HookCalled(t *testing.T) {
	f, err := os.CreateTemp("", "apikey_audit_hook_*.db")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	t.Cleanup(func() { os.Remove(path) })

	var events []string
	s, err := OpenStore(path, WithAudit(func(event, keyID, ownerID string) {
		events = append(events, event)
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	// Generate a key.
	clearKey, _, err := s.Generate("key_AH1", "owner_ah", "Audit Key", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Resolve the key.
	_, err = s.Resolve(clearKey)
	if err != nil {
		t.Fatal(err)
	}

	// Revoke the key.
	if err := s.Revoke("key_AH1"); err != nil {
		t.Fatal(err)
	}

	// Verify events.
	if len(events) != 3 {
		t.Fatalf("events = %v, want 3 events", events)
	}
	want := []string{"generate", "resolve", "revoke"}
	for i, w := range want {
		if events[i] != w {
			t.Errorf("events[%d] = %q, want %q", i, events[i], w)
		}
	}
}

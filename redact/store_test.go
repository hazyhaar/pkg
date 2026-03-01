package redact

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func setupTestStore(t *testing.T, opts ...StoreOption) (*sql.DB, *Store) {
	t.Helper()
	db := setupTestDB(t)
	s := NewStore(db, opts...)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	return db, s
}

func TestStoreInit_CreatesTable(t *testing.T) {
	db := setupTestDB(t)
	s := NewStore(db)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, table := range []string{"redact_patterns", "redact_whitelist"} {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Fatalf("table %q not created: %v", table, err)
		}
	}
}

func TestStoreInit_Idempotent(t *testing.T) {
	db := setupTestDB(t)
	s := NewStore(db)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("second Init should be idempotent: %v", err)
	}
}

func TestStore_AddPatternAndReload(t *testing.T) {
	_, s := setupTestStore(t)

	if err := s.AddPattern("email", `\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z|a-z]{2,}\b`, "[email]"); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}

	got := s.Sanitize("contact user@example.com for help")
	if strings.Contains(got, "user@example.com") {
		t.Fatalf("email not redacted: %s", got)
	}
	if !strings.Contains(got, "[email]") {
		t.Fatalf("expected [email] placeholder, got: %s", got)
	}
}

func TestStore_RemovePatternAndReload(t *testing.T) {
	_, s := setupTestStore(t)

	if err := s.AddPattern("email", `\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z|a-z]{2,}\b`, "[email]"); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}

	// Deactivate.
	if err := s.RemovePattern("email"); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}

	got := s.Sanitize("contact user@example.com for help")
	if !strings.Contains(got, "user@example.com") {
		t.Fatalf("deactivated pattern should not redact: %s", got)
	}
}

func TestStore_WhitelistPreservesMatch(t *testing.T) {
	_, s := setupTestStore(t, WithStaticRules(Defaults()))

	// The default rules would redact IP addresses.
	// Add a whitelist for a specific IP.
	if err := s.AddWhitelist("known_server", `10\.0\.0\.42`); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}

	got := s.Sanitize("connect to 10.0.0.42:8080 and 192.168.1.1:80")
	// The whitelisted IP should be preserved.
	if !strings.Contains(got, "10.0.0.42") {
		t.Fatalf("whitelisted IP should be preserved: %s", got)
	}
	// The non-whitelisted IP should still be redacted.
	if strings.Contains(got, "192.168.1.1") {
		t.Fatalf("non-whitelisted IP should be redacted: %s", got)
	}
}

func TestStore_RemoveWhitelistAndReload(t *testing.T) {
	_, s := setupTestStore(t, WithStaticRules(Defaults()))

	if err := s.AddWhitelist("known_server", `10\.0\.0\.42`); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}

	// Now remove the whitelist.
	if err := s.RemoveWhitelist("known_server"); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}

	got := s.Sanitize("connect to 10.0.0.42:8080")
	// Should now be redacted.
	if strings.Contains(got, "10.0.0.42") {
		t.Fatalf("IP should be redacted after whitelist removal: %s", got)
	}
}

func TestStore_InvalidPatternRejected(t *testing.T) {
	_, s := setupTestStore(t)
	err := s.AddPattern("bad", `[invalid`, "[x]")
	if err == nil {
		t.Fatal("invalid regex should be rejected")
	}
}

func TestStore_InvalidPatternInDB_Skipped(t *testing.T) {
	db, s := setupTestStore(t)
	// Insert invalid regex directly into DB (bypassing validation).
	_, _ = db.Exec(`INSERT INTO redact_patterns (name, pattern, replace_with) VALUES ('bad', '[invalid', '[x]')`)

	// Reload should succeed, skipping the bad pattern.
	if err := s.Reload(); err != nil {
		t.Fatalf("Reload should skip bad patterns: %v", err)
	}

	// Should still work (just with no dynamic rules).
	got := s.Sanitize("hello world")
	if got != "hello world" {
		t.Fatalf("sanitize should be no-op with no valid rules: %s", got)
	}
}

func TestStore_InvalidWhitelistInDB_Skipped(t *testing.T) {
	db, s := setupTestStore(t)
	_, _ = db.Exec(`INSERT INTO redact_whitelist (name, pattern) VALUES ('bad', '[invalid')`)

	if err := s.Reload(); err != nil {
		t.Fatalf("Reload should skip bad whitelist patterns: %v", err)
	}
}

func TestStore_StaticAndDynamicRulesCombined(t *testing.T) {
	_, s := setupTestStore(t, WithStaticRules(Defaults()))

	// Add a custom dynamic rule.
	if err := s.AddPattern("siren", `\b\d{9}\b`, "[SIREN]"); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}

	got := s.Sanitize("entreprise 123456789 at 10.0.0.1:80")
	if !strings.Contains(got, "[SIREN]") {
		t.Fatalf("dynamic rule not applied: %s", got)
	}
	if strings.Contains(got, "10.0.0.1") {
		t.Fatalf("static rule not applied: %s", got)
	}
}

func TestStore_ListPatterns(t *testing.T) {
	_, s := setupTestStore(t)
	_ = s.AddPattern("a", `aaa`, "[a]")
	_ = s.AddPattern("b", `bbb`, "[b]")
	_ = s.RemovePattern("b")

	entries, err := s.ListPatterns()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Check that 'a' is active and 'b' is not.
	for _, e := range entries {
		if e.Name == "a" && !e.IsActive {
			t.Fatal("pattern 'a' should be active")
		}
		if e.Name == "b" && e.IsActive {
			t.Fatal("pattern 'b' should be inactive")
		}
	}
}

func TestStore_ListWhitelist(t *testing.T) {
	_, s := setupTestStore(t)
	_ = s.AddWhitelist("x", `xxx`)
	_ = s.AddWhitelist("y", `yyy`)
	_ = s.RemoveWhitelist("y")

	entries, err := s.ListWhitelist()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Name == "x" && !e.IsActive {
			t.Fatal("whitelist 'x' should be active")
		}
		if e.Name == "y" && e.IsActive {
			t.Fatal("whitelist 'y' should be inactive")
		}
	}
}

func TestStore_AddPatternUpsert(t *testing.T) {
	_, s := setupTestStore(t)

	if err := s.AddPattern("test", `aaa`, "[a]"); err != nil {
		t.Fatal(err)
	}
	// Update the same pattern.
	if err := s.AddPattern("test", `bbb`, "[b]"); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}

	// Should use the updated pattern.
	got := s.Sanitize("bbb")
	if !strings.Contains(got, "[b]") {
		t.Fatalf("upserted pattern not applied: %s", got)
	}
	// Old pattern should not match.
	got = s.Sanitize("aaa")
	if strings.Contains(got, "[a]") {
		t.Fatalf("old pattern should not match: %s", got)
	}
}

func TestStore_EmptyDB_NoRules(t *testing.T) {
	_, s := setupTestStore(t)
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	got := s.Sanitize("sensitive Bearer token at 10.0.0.1")
	// No static rules, no dynamic rules — no redaction.
	if got != "sensitive Bearer token at 10.0.0.1" {
		t.Fatalf("empty store should be a no-op: %s", got)
	}
}

func TestStore_WhitelistAndBlacklistInteraction(t *testing.T) {
	_, s := setupTestStore(t)

	// Blacklist: redact anything that looks like an IP.
	if err := s.AddPattern("ips", `\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`, "[addr]"); err != nil {
		t.Fatal(err)
	}
	// Whitelist: but preserve 127.0.0.1 (localhost).
	if err := s.AddWhitelist("localhost", `127\.0\.0\.1`); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}

	got := s.Sanitize("call 10.0.0.5 and 127.0.0.1")
	if strings.Contains(got, "10.0.0.5") {
		t.Fatalf("non-whitelisted IP should be redacted: %s", got)
	}
	if !strings.Contains(got, "127.0.0.1") {
		t.Fatalf("whitelisted IP should be preserved: %s", got)
	}
}

func TestStore_ReactivatePattern(t *testing.T) {
	_, s := setupTestStore(t)

	_ = s.AddPattern("test", `secret_value`, "[redacted]")
	_ = s.RemovePattern("test")
	_ = s.Reload()

	// Not redacted after removal.
	got := s.Sanitize("found secret_value here")
	if strings.Contains(got, "[redacted]") {
		t.Fatal("deactivated pattern should not redact")
	}

	// Re-add (upsert reactivates).
	_ = s.AddPattern("test", `secret_value`, "[redacted]")
	_ = s.Reload()

	got = s.Sanitize("found secret_value here")
	if !strings.Contains(got, "[redacted]") {
		t.Fatal("reactivated pattern should redact")
	}
}

// CLAUDE:SUMMARY SQLite-backed runtime-updatable redaction engine with blacklist and whitelist pattern management.
// CLAUDE:DEPENDS
// CLAUDE:EXPORTS Store, NewStore, StoreOption, WithStaticRules, StoreSchema, PatternEntry, WhitelistEntry
package redact

import (
	"database/sql"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
)

// StoreSchema creates the tables for runtime-manageable redaction rules.
//
// Table redact_patterns: blacklist patterns (things to redact).
// Table redact_whitelist: whitelist patterns (things to preserve, skip redaction).
//
// Both tables support is_active for enable/disable without deletion.
const StoreSchema = `
CREATE TABLE IF NOT EXISTS redact_patterns (
	name TEXT PRIMARY KEY,
	pattern TEXT NOT NULL,
	replace_with TEXT NOT NULL DEFAULT '[redacted]',
	is_active INTEGER NOT NULL DEFAULT 1 CHECK(is_active IN (0, 1)),
	created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
	updated_at INTEGER
);

CREATE TABLE IF NOT EXISTS redact_whitelist (
	name TEXT PRIMARY KEY,
	pattern TEXT NOT NULL,
	is_active INTEGER NOT NULL DEFAULT 1 CHECK(is_active IN (0, 1)),
	created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
	updated_at INTEGER
);
`

// Store is a SQLite-backed, runtime-updatable redaction engine.
// It loads blacklist patterns (what to redact) and whitelist patterns
// (what to preserve) from the database. Patterns can be added, removed,
// or toggled at runtime and reloaded without restart.
type Store struct {
	db        *sql.DB
	static    []Rule // compiled static rules (Defaults, etc.)
	blacklist []Rule // compiled from redact_patterns
	whitelist []*regexp.Regexp
	mu        sync.RWMutex
}

// StoreOption configures a Store.
type StoreOption func(*Store)

// WithStaticRules sets the static (code-defined) rules that are always
// applied in addition to the dynamic database rules.
func WithStaticRules(rules ...[]Rule) StoreOption {
	return func(s *Store) {
		for _, rs := range rules {
			s.static = append(s.static, rs...)
		}
	}
}

// NewStore creates a Store backed by the given database.
// Call Init() to create the tables, then Reload() to load patterns.
func NewStore(db *sql.DB, opts ...StoreOption) *Store {
	s := &Store{db: db}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Init creates the redact_patterns and redact_whitelist tables.
func (s *Store) Init() error {
	_, err := s.db.Exec(StoreSchema)
	return err
}

// Reload reads all active patterns from the database and compiles them.
// Invalid regex patterns are logged and skipped.
func (s *Store) Reload() error {
	blacklist, err := s.loadBlacklist()
	if err != nil {
		return fmt.Errorf("redact: load blacklist: %w", err)
	}

	whitelist, err := s.loadWhitelist()
	if err != nil {
		return fmt.Errorf("redact: load whitelist: %w", err)
	}

	s.mu.Lock()
	s.blacklist = blacklist
	s.whitelist = whitelist
	s.mu.Unlock()

	slog.Info("redact: rules reloaded", "blacklist", len(blacklist), "whitelist", len(whitelist))
	return nil
}

func (s *Store) loadBlacklist() ([]Rule, error) {
	rows, err := s.db.Query(`SELECT name, pattern, replace_with FROM redact_patterns WHERE is_active = 1 ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []Rule
	for rows.Next() {
		var name, pattern, replace string
		if err := rows.Scan(&name, &pattern, &replace); err != nil {
			return nil, err
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			slog.Warn("redact: invalid blacklist pattern, skipping", "name", name, "error", err)
			continue
		}
		rules = append(rules, Rule{Name: name, Pattern: re, Replace: replace})
	}
	return rules, rows.Err()
}

func (s *Store) loadWhitelist() ([]*regexp.Regexp, error) {
	rows, err := s.db.Query(`SELECT name, pattern FROM redact_whitelist WHERE is_active = 1 ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var patterns []*regexp.Regexp
	for rows.Next() {
		var name, pattern string
		if err := rows.Scan(&name, &pattern); err != nil {
			return nil, err
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			slog.Warn("redact: invalid whitelist pattern, skipping", "name", name, "error", err)
			continue
		}
		patterns = append(patterns, re)
	}
	return patterns, rows.Err()
}

// Sanitize applies the full pipeline: static rules + dynamic blacklist,
// but preserves substrings matching any whitelist pattern.
//
// Order: whitelisted substrings are temporarily replaced with placeholders,
// then all rules (static + dynamic) are applied, then placeholders are
// restored.
func (s *Store) Sanitize(input string) string {
	s.mu.RLock()
	blacklist := s.blacklist
	whitelist := s.whitelist
	static := s.static
	s.mu.RUnlock()

	// Step 1: find and protect whitelisted substrings.
	type preserved struct {
		placeholder string
		value       string
	}
	var preserves []preserved
	result := input
	for i, wl := range whitelist {
		matches := wl.FindAllString(result, -1)
		for j, m := range matches {
			ph := fmt.Sprintf("\x00WL%d_%d\x00", i, j)
			preserves = append(preserves, preserved{placeholder: ph, value: m})
			// Replace first occurrence only per match to handle duplicates.
			result = replaceFirst(result, m, ph)
		}
	}

	// Step 2: apply static rules.
	for _, rule := range static {
		result = rule.Pattern.ReplaceAllString(result, rule.Replace)
	}

	// Step 3: apply dynamic blacklist rules.
	for _, rule := range blacklist {
		result = rule.Pattern.ReplaceAllString(result, rule.Replace)
	}

	// Step 4: restore whitelisted substrings.
	for _, p := range preserves {
		result = replaceFirst(result, p.placeholder, p.value)
	}

	return result
}

func replaceFirst(s, old, new string) string {
	i := indexOf(s, old)
	if i < 0 {
		return s
	}
	return s[:i] + new + s[i+len(old):]
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// AddPattern inserts or replaces a blacklist pattern in the database.
// Call Reload() after to pick up changes.
func (s *Store) AddPattern(name, pattern, replace string) error {
	// Validate regex before inserting.
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("redact: invalid pattern: %w", err)
	}
	_, err := s.db.Exec(`INSERT INTO redact_patterns (name, pattern, replace_with)
		VALUES (?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET pattern=excluded.pattern, replace_with=excluded.replace_with,
		updated_at=strftime('%s','now'), is_active=1`,
		name, pattern, replace)
	return err
}

// RemovePattern deactivates a blacklist pattern.
func (s *Store) RemovePattern(name string) error {
	_, err := s.db.Exec(`UPDATE redact_patterns SET is_active = 0, updated_at = strftime('%s','now') WHERE name = ?`, name)
	return err
}

// AddWhitelist inserts or replaces a whitelist pattern in the database.
func (s *Store) AddWhitelist(name, pattern string) error {
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("redact: invalid whitelist pattern: %w", err)
	}
	_, err := s.db.Exec(`INSERT INTO redact_whitelist (name, pattern)
		VALUES (?, ?)
		ON CONFLICT(name) DO UPDATE SET pattern=excluded.pattern,
		updated_at=strftime('%s','now'), is_active=1`,
		name, pattern)
	return err
}

// RemoveWhitelist deactivates a whitelist pattern.
func (s *Store) RemoveWhitelist(name string) error {
	_, err := s.db.Exec(`UPDATE redact_whitelist SET is_active = 0, updated_at = strftime('%s','now') WHERE name = ?`, name)
	return err
}

// ListPatterns returns all blacklist patterns (active and inactive).
func (s *Store) ListPatterns() ([]PatternEntry, error) {
	rows, err := s.db.Query(`SELECT name, pattern, replace_with, is_active FROM redact_patterns ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []PatternEntry
	for rows.Next() {
		var e PatternEntry
		var active int
		if err := rows.Scan(&e.Name, &e.Pattern, &e.Replace, &active); err != nil {
			return nil, err
		}
		e.IsActive = active == 1
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// ListWhitelist returns all whitelist patterns (active and inactive).
func (s *Store) ListWhitelist() ([]WhitelistEntry, error) {
	rows, err := s.db.Query(`SELECT name, pattern, is_active FROM redact_whitelist ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []WhitelistEntry
	for rows.Next() {
		var e WhitelistEntry
		var active int
		if err := rows.Scan(&e.Name, &e.Pattern, &active); err != nil {
			return nil, err
		}
		e.IsActive = active == 1
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// PatternEntry is a row from redact_patterns.
type PatternEntry struct {
	Name     string
	Pattern  string
	Replace  string
	IsActive bool
}

// WhitelistEntry is a row from redact_whitelist.
type WhitelistEntry struct {
	Name     string
	Pattern  string
	IsActive bool
}

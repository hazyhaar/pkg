// CLAUDE:SUMMARY Regex-based string sanitizer that strips tokens, paths, IPs, stack traces, and encoded secrets before LLM exposure.
// CLAUDE:DEPENDS
// CLAUDE:EXPORTS Redactor, New, Rule, Defaults, SQLitePaths, Custom, Merge, SanitizeError, ContainsSensitive, StripGoStackTraces, MustCompileRule

// Package redact sanitizes error messages and strings before they are
// returned to an LLM. It strips sensitive information such as Bearer tokens,
// API keys, file paths, IP addresses, stack traces, and long base64 strings.
package redact

import (
	"regexp"
	"strings"
)

// Rule defines a single redaction pattern.
type Rule struct {
	Name    string
	Pattern *regexp.Regexp
	Replace string
}

// Redactor applies a pipeline of rules to sanitize strings.
type Redactor struct {
	rules []Rule
}

// New creates a Redactor from the given rule slices (merged in order).
func New(ruleSets ...[]Rule) *Redactor {
	var all []Rule
	for _, rs := range ruleSets {
		all = append(all, rs...)
	}
	return &Redactor{rules: all}
}

// Sanitize applies all rules in order and returns the cleaned string.
func (r *Redactor) Sanitize(s string) string {
	for _, rule := range r.rules {
		s = rule.Pattern.ReplaceAllString(s, rule.Replace)
	}
	return s
}

// Custom creates a single-rule slice for use with New.
func Custom(name, pattern, replace string) []Rule {
	return []Rule{{Name: name, Pattern: regexp.MustCompile(pattern), Replace: replace}}
}

// Defaults returns the standard set of redaction rules covering tokens,
// paths, addresses, stack traces, and encoded strings.
func Defaults() []Rule {
	return []Rule{
		bearerToken,
		apiKeyParam,
		authorizationHeader,
		unixPath,
		windowsPath,
		ipv6Addr,
		ipv4Port,
		goStackTrace,
		base64Long,
	}
}

// SQLitePaths returns rules that redact SQLite database file paths.
func SQLitePaths() []Rule {
	return []Rule{sqlitePath}
}

// --- default rules ---

var bearerToken = Rule{
	Name:    "bearer_token",
	Pattern: regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9\-._~+/]+=*`),
	Replace: "Bearer [token]",
}

var apiKeyParam = Rule{
	Name:    "api_key_param",
	Pattern: regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password)\s*=\s*[^\s&]+`),
	Replace: "${1}=[key]",
}

var authorizationHeader = Rule{
	Name:    "authorization_header",
	Pattern: regexp.MustCompile(`(?i)Authorization:\s*\S+`),
	Replace: "Authorization: [redacted]",
}

var unixPath = Rule{
	Name:    "unix_path",
	Pattern: regexp.MustCompile(`(/(?:home|usr|var|tmp|etc|opt|root|srv|proc|sys|mnt|media|run|dev|snap|nix|Applications|Users|Library|Volumes)(?:/[^\s:,"']+)+)`),
	Replace: "[path]",
}

var windowsPath = Rule{
	Name:    "windows_path",
	Pattern: regexp.MustCompile(`[A-Z]:\\(?:[^\s:,"']+\\)*[^\s:,"']+`),
	Replace: "[path]",
}

var ipv4Port = Rule{
	Name:    "ipv4_port",
	Pattern: regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(:\d+)?\b`),
	Replace: "[addr]",
}

var ipv6Addr = Rule{
	Name:    "ipv6_addr",
	Pattern: regexp.MustCompile(`\[?(?:[0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}(?:%[a-zA-Z0-9]+)?\]?(:\d+)?`),
	Replace: "[addr]",
}

var goStackTrace = Rule{
	Name:    "go_stack_trace",
	Pattern: regexp.MustCompile(`goroutine \d+[^\n]*\n(?:[\t ]+\S+\.go:\d+[^\n]*\n?)+`),
	Replace: "[trace]",
}

var base64Long = Rule{
	Name:    "base64_long",
	Pattern: regexp.MustCompile(`[A-Za-z0-9+/]{40,}={0,2}`),
	Replace: "[encoded]",
}

var sqlitePath = Rule{
	Name:    "sqlite_path",
	Pattern: regexp.MustCompile(`(?:/[^\s:,"']+)+\.(?:db|sqlite|sqlite3)`),
	Replace: "[db]",
}

// SanitizeError is a convenience function that applies Defaults() to an error message.
func SanitizeError(msg string) string {
	return New(Defaults()).Sanitize(msg)
}

// String returns a human-readable summary of a Rule.
func (r Rule) String() string {
	return r.Name + ": " + r.Pattern.String() + " -> " + r.Replace
}

// Rules returns the rules currently configured in the Redactor.
func (r *Redactor) Rules() []Rule {
	out := make([]Rule, len(r.rules))
	copy(out, r.rules)
	return out
}

// Wrap returns a new Redactor that prepends additional rules before the
// existing ones. Useful for layering project-specific rules on top of defaults.
func (r *Redactor) Wrap(extra ...[]Rule) *Redactor {
	var all []Rule
	for _, rs := range extra {
		all = append(all, rs...)
	}
	all = append(all, r.rules...)
	return &Redactor{rules: all}
}

// StripGoStackTraces is a fast-path helper that only removes Go stack traces
// without applying the full rule set.
func StripGoStackTraces(s string) string {
	return goStackTrace.Pattern.ReplaceAllString(s, "[trace]")
}

// ContainsSensitive returns true if any default rule matches in the string.
// Useful for pre-checks before logging or returning errors.
func ContainsSensitive(s string) bool {
	for _, rule := range Defaults() {
		if rule.Pattern.MatchString(s) {
			return true
		}
	}
	return false
}

// RedactMap applies the redactor to all string values in a map (shallow).
// Non-string values are left as-is. Returns a new map.
func (r *Redactor) RedactMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = r.Sanitize(v)
	}
	return out
}

// MustCompileRule creates a Rule, panicking if the pattern is invalid.
// Intended for package-level var declarations.
func MustCompileRule(name, pattern, replace string) Rule {
	return Rule{
		Name:    name,
		Pattern: regexp.MustCompile(pattern),
		Replace: replace,
	}
}

// Merge combines multiple rule slices into a single slice.
func Merge(ruleSets ...[]Rule) []Rule {
	var all []Rule
	for _, rs := range ruleSets {
		all = append(all, rs...)
	}
	return all
}

// SanitizeLines applies the redactor to each line of a multi-line string,
// preserving the line structure. Empty lines are kept.
func (r *Redactor) SanitizeLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = r.Sanitize(line)
	}
	return strings.Join(lines, "\n")
}

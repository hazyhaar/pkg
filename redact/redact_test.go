package redact

import (
	"strings"
	"testing"
)

func TestDefaults_BearerToken(t *testing.T) {
	r := New(Defaults())
	got := r.Sanitize("error: Bearer eyJhbGciOiJIUzI1NiJ9.token.sig failed")
	if strings.Contains(got, "eyJ") {
		t.Fatalf("bearer token not redacted: %s", got)
	}
	if !strings.Contains(got, "Bearer [token]") {
		t.Fatalf("expected 'Bearer [token]', got: %s", got)
	}
}

func TestDefaults_APIKeyParam(t *testing.T) {
	r := New(Defaults())

	tests := []struct {
		input string
		must  string // must contain after redaction
	}{
		{"api_key=sk-1234abcd", "api_key=[key]"},
		{"secret=mysupersecret", "secret=[key]"},
		{"token=abc123&foo=bar", "token=[key]"},
		{"API-KEY=deadbeef", "API-KEY=[key]"},
	}
	for _, tt := range tests {
		got := r.Sanitize(tt.input)
		if !strings.Contains(got, tt.must) {
			t.Errorf("Sanitize(%q) = %q, want to contain %q", tt.input, got, tt.must)
		}
	}
}

func TestDefaults_AuthorizationHeader(t *testing.T) {
	r := New(Defaults())
	got := r.Sanitize("Authorization: Basic dXNlcjpwYXNz in headers")
	if !strings.Contains(got, "Authorization: [redacted]") {
		t.Fatalf("authorization header not redacted: %s", got)
	}
}

func TestDefaults_UnixPath(t *testing.T) {
	r := New(Defaults())

	tests := []string{
		"/home/user/data/app.db",
		"/usr/local/bin/myapp",
		"/var/log/syslog",
		"/tmp/session-abc123",
		"/Users/john/Documents/file.txt",
	}
	for _, path := range tests {
		got := r.Sanitize("open " + path + ": no such file")
		if strings.Contains(got, path) {
			t.Errorf("unix path not redacted: %s", got)
		}
		if !strings.Contains(got, "[path]") {
			t.Errorf("expected [path] placeholder, got: %s", got)
		}
	}
}

func TestDefaults_WindowsPath(t *testing.T) {
	r := New(Defaults())
	got := r.Sanitize(`open C:\Users\Admin\data\app.db: access denied`)
	if strings.Contains(got, `C:\Users`) {
		t.Fatalf("windows path not redacted: %s", got)
	}
	if !strings.Contains(got, "[path]") {
		t.Fatalf("expected [path] placeholder, got: %s", got)
	}
}

func TestDefaults_IPv4Port(t *testing.T) {
	r := New(Defaults())

	tests := []struct {
		input string
	}{
		{"connect to 192.168.1.100:5432 failed"},
		{"server at 10.0.0.5:8080 is down"},
		{"host 172.16.0.1 unreachable"},
	}
	for _, tt := range tests {
		got := r.Sanitize(tt.input)
		if !strings.Contains(got, "[addr]") {
			t.Errorf("IP not redacted in %q: %s", tt.input, got)
		}
	}
}

func TestDefaults_IPv6Addr(t *testing.T) {
	r := New(Defaults())
	got := r.Sanitize("connect to [2001:db8::1]:443 failed")
	if strings.Contains(got, "2001:db8") {
		t.Fatalf("IPv6 not redacted: %s", got)
	}
	if !strings.Contains(got, "[addr]") {
		t.Fatalf("expected [addr] placeholder, got: %s", got)
	}
}

func TestDefaults_GoStackTrace(t *testing.T) {
	r := New(Defaults())
	input := `error occurred
goroutine 1 [running]:
	main.go:42
	handler.go:99
some more text`

	got := r.Sanitize(input)
	if strings.Contains(got, "goroutine") {
		t.Fatalf("stack trace not redacted: %s", got)
	}
	if !strings.Contains(got, "[trace]") {
		t.Fatalf("expected [trace] placeholder, got: %s", got)
	}
	if !strings.Contains(got, "some more text") {
		t.Fatalf("non-trace text should be preserved: %s", got)
	}
}

func TestDefaults_Base64Long(t *testing.T) {
	r := New(Defaults())
	b64 := "YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY3ODk="
	got := r.Sanitize("data: " + b64 + " end")
	if strings.Contains(got, b64) {
		t.Fatalf("base64 not redacted: %s", got)
	}
	if !strings.Contains(got, "[encoded]") {
		t.Fatalf("expected [encoded] placeholder, got: %s", got)
	}
}

func TestDefaults_ShortBase64NotRedacted(t *testing.T) {
	r := New(Defaults())
	// Under 40 characters should not be redacted.
	got := r.Sanitize("id: abc123XYZ")
	if strings.Contains(got, "[encoded]") {
		t.Fatalf("short base64-like string should not be redacted: %s", got)
	}
}

func TestSQLitePaths(t *testing.T) {
	r := New(Defaults(), SQLitePaths())
	got := r.Sanitize("open /data/myapp/store.db: locked")
	if strings.Contains(got, "store.db") {
		t.Fatalf("sqlite path not redacted: %s", got)
	}
	if !strings.Contains(got, "[db]") {
		t.Fatalf("expected [db] placeholder, got: %s", got)
	}
}

func TestSQLitePaths_Extensions(t *testing.T) {
	r := New(SQLitePaths())

	tests := []string{
		"/data/app.sqlite",
		"/data/app.sqlite3",
		"/data/app.db",
	}
	for _, p := range tests {
		got := r.Sanitize("open " + p + ": error")
		if !strings.Contains(got, "[db]") {
			t.Errorf("sqlite path %q not redacted: %s", p, got)
		}
	}
}

func TestCustomRule(t *testing.T) {
	r := New(Defaults(), Custom("siren", `\b\d{9}\b`, "[SIREN]"))
	got := r.Sanitize("entreprise 123456789 not found")
	if !strings.Contains(got, "[SIREN]") {
		t.Fatalf("custom rule not applied: %s", got)
	}
}

func TestSanitizeError(t *testing.T) {
	got := SanitizeError("open /home/user/data.db: Bearer sk-abc123 failed")
	if strings.Contains(got, "/home/user") {
		t.Fatalf("SanitizeError did not redact path: %s", got)
	}
}

func TestContainsSensitive(t *testing.T) {
	if !ContainsSensitive("Bearer abc123") {
		t.Fatal("expected sensitive content detected")
	}
	if ContainsSensitive("normal error message") {
		t.Fatal("false positive on normal string")
	}
}

func TestStripGoStackTraces(t *testing.T) {
	input := "error\ngoroutine 5 [running]:\n\tmain.go:10\nend"
	got := StripGoStackTraces(input)
	if strings.Contains(got, "goroutine") {
		t.Fatalf("trace not stripped: %s", got)
	}
	if !strings.Contains(got, "error") || !strings.Contains(got, "end") {
		t.Fatalf("non-trace text lost: %s", got)
	}
}

func TestRedactMap(t *testing.T) {
	r := New(Defaults())
	m := map[string]string{
		"error":  "connect to 10.0.0.1:5432 failed",
		"detail": "normal message",
	}
	got := r.RedactMap(m)
	if !strings.Contains(got["error"], "[addr]") {
		t.Fatalf("map value not redacted: %s", got["error"])
	}
	if got["detail"] != "normal message" {
		t.Fatalf("clean value changed: %s", got["detail"])
	}
}

func TestWrap(t *testing.T) {
	base := New(Defaults())
	wrapped := base.Wrap(Custom("siren", `\b\d{9}\b`, "[SIREN]"))

	got := wrapped.Sanitize("entreprise 123456789 at 10.0.0.1:80")
	if !strings.Contains(got, "[SIREN]") {
		t.Fatalf("custom rule not applied via Wrap: %s", got)
	}
	if !strings.Contains(got, "[addr]") {
		t.Fatalf("default rule not applied via Wrap: %s", got)
	}
}

func TestSanitizeLines(t *testing.T) {
	r := New(Defaults())
	input := "line1 Bearer sk-test\nline2 ok\nline3 10.0.0.1:80"
	got := r.SanitizeLines(input)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "Bearer [token]") {
		t.Fatalf("line 1 not redacted: %s", lines[0])
	}
	if lines[1] != "line2 ok" {
		t.Fatalf("line 2 changed: %s", lines[1])
	}
	if !strings.Contains(lines[2], "[addr]") {
		t.Fatalf("line 3 not redacted: %s", lines[2])
	}
}

func TestMerge(t *testing.T) {
	merged := Merge(Defaults(), SQLitePaths())
	r := New(merged)
	got := r.Sanitize("open /data/app.db at 10.0.0.1")
	if !strings.Contains(got, "[db]") || !strings.Contains(got, "[addr]") {
		t.Fatalf("merged rules not applied: %s", got)
	}
}

func TestRules(t *testing.T) {
	r := New(Defaults())
	rules := r.Rules()
	if len(rules) != len(Defaults()) {
		t.Fatalf("Rules() returned %d rules, want %d", len(rules), len(Defaults()))
	}
}

func TestEmptyRedactor(t *testing.T) {
	r := New()
	input := "Bearer token at /home/user/file"
	if got := r.Sanitize(input); got != input {
		t.Fatalf("empty redactor should be a no-op, got: %s", got)
	}
}

func TestNoFalsePositiveOnNormalText(t *testing.T) {
	r := New(Defaults())
	normal := "SELECT * FROM users WHERE name = 'alice'"
	got := r.Sanitize(normal)
	if got != normal {
		t.Fatalf("normal SQL should not be modified, got: %s", got)
	}
}

func TestMultipleRedactionsInOneLine(t *testing.T) {
	r := New(Defaults())
	input := "Bearer sk-abc at 192.168.1.1:5432 with api_key=secret123"
	got := r.Sanitize(input)
	if strings.Contains(got, "sk-abc") {
		t.Fatalf("bearer token not redacted: %s", got)
	}
	if strings.Contains(got, "192.168.1.1") {
		t.Fatalf("IP not redacted: %s", got)
	}
	if strings.Contains(got, "secret123") {
		t.Fatalf("api key not redacted: %s", got)
	}
}

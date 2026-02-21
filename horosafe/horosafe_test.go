package horosafe

import (
	"bytes"
	"net"
	"strings"
	"testing"
)

func TestValidateSecret(t *testing.T) {
	if err := ValidateSecret([]byte("short")); err == nil {
		t.Fatal("expected error for short secret")
	}
	if err := ValidateSecret(bytes.Repeat([]byte("a"), MinSecretLen)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSafePath(t *testing.T) {
	tests := []struct {
		base, input string
		wantErr     bool
	}{
		{"/data/chunks", "abc/def", false},
		{"/data/chunks", "../etc/passwd", true},
		{"/data/chunks", "abc/../def", true},
		{"/data/chunks", "abc/../../outside", true},
		{"/data/chunks", "normal-id_123", false},
	}
	for _, tt := range tests {
		_, err := SafePath(tt.base, tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("SafePath(%q, %q) error=%v, wantErr=%v", tt.base, tt.input, err, tt.wantErr)
		}
	}
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{"https://example.com/webhook", false},
		{"http://example.com/hook", false},
		{"ftp://evil.com/data", true},      // bad scheme
		{"javascript:alert(1)", true},      // bad scheme
		{"http://127.0.0.1/admin", true},   // loopback
		{"http://10.0.0.1/internal", true}, // private
		{"http://192.168.1.1/api", true},   // private
		{"http://[::1]/api", true},         // IPv6 loopback
		{"http://172.16.0.1/secret", true}, // private
	}
	for _, tt := range tests {
		err := ValidateURL(tt.url)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateURL(%q) error=%v, wantErr=%v", tt.url, err, tt.wantErr)
		}
	}
}

func TestValidateIdentifier(t *testing.T) {
	if err := ValidateIdentifier("valid-id_123.txt"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ValidateIdentifier("../etc/passwd"); err == nil {
		t.Fatal("expected error for path traversal chars")
	}
	if err := ValidateIdentifier(""); err == nil {
		t.Fatal("expected error for empty identifier")
	}
	if err := ValidateIdentifier("has spaces"); err == nil {
		t.Fatal("expected error for spaces")
	}
	long := strings.Repeat("a", 257)
	if err := ValidateIdentifier(long); err == nil {
		t.Fatal("expected error for long identifier")
	}
}

func TestLimitedReadAll(t *testing.T) {
	data := strings.Repeat("x", 100)
	got, err := LimitedReadAll(strings.NewReader(data), 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 100 {
		t.Fatalf("expected 100 bytes, got %d", len(got))
	}

	_, err = LimitedReadAll(strings.NewReader(data), 50)
	if err == nil {
		t.Fatal("expected error for oversized read")
	}
}

func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		ip      string
		private bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.0.1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"::1", true},
	}
	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		if ip == nil {
			t.Fatalf("failed to parse IP %q", tt.ip)
		}
		if got := isPrivateIP(ip); got != tt.private {
			t.Errorf("isPrivateIP(%s) = %v, want %v", tt.ip, got, tt.private)
		}
	}
}

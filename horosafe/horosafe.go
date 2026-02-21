// Package horosafe provides security primitives shared across the HOROS
// service ecosystem: secret validation, URL safety checks (SSRF prevention),
// path traversal guards, and bounded I/O helpers.
package horosafe

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"strings"
)

// MinSecretLen is the minimum acceptable length for symmetric secrets (HMAC,
// JWT HS256, webhook signatures). 32 bytes = 256 bits of entropy.
const MinSecretLen = 32

// MaxResponseBody is the default cap for HTTP response body reads (1 MiB).
const MaxResponseBody int64 = 1 << 20

// ErrSecretTooShort is returned when a secret does not meet MinSecretLen.
var ErrSecretTooShort = fmt.Errorf("horosafe: secret must be at least %d bytes", MinSecretLen)

// ErrPathTraversal is returned when a user-supplied path escapes its base.
var ErrPathTraversal = errors.New("horosafe: path traversal detected")

// ErrSSRF is returned when a URL targets a private/loopback address.
var ErrSSRF = errors.New("horosafe: URL targets a private or loopback address")

// ErrUnsafeScheme is returned when a URL uses a non-HTTP(S) scheme.
var ErrUnsafeScheme = errors.New("horosafe: only http and https schemes are allowed")

// ValidateSecret checks that secret is at least MinSecretLen bytes.
func ValidateSecret(secret []byte) error {
	if len(secret) < MinSecretLen {
		return ErrSecretTooShort
	}
	return nil
}

// SafePath validates that joining base and userInput does not escape base.
// Returns the cleaned absolute path or ErrPathTraversal.
func SafePath(base, userInput string) (string, error) {
	if strings.Contains(userInput, "..") {
		return "", ErrPathTraversal
	}
	// Clean both and verify the result stays under base.
	cleaned := filepath.Join(base, filepath.Clean("/"+userInput))
	if !strings.HasPrefix(cleaned, filepath.Clean(base)+string(filepath.Separator)) &&
		cleaned != filepath.Clean(base) {
		return "", ErrPathTraversal
	}
	return cleaned, nil
}

// ValidateURL checks that rawURL uses http/https, has a hostname, and does
// not resolve to a private or loopback IP (SSRF prevention).
// DNS resolution is performed to catch rebinding via internal hostnames.
func ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("horosafe: invalid URL: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ErrUnsafeScheme
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("horosafe: URL has no host")
	}

	// Check literal IP first.
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(ip) {
			return ErrSSRF
		}
		return nil
	}

	// Resolve hostname and check all addresses.
	addrs, err := net.LookupHost(host)
	if err != nil {
		// DNS failure — allow through (might be a valid external host that
		// is temporarily unresolvable). The caller will get a network error
		// at connection time anyway.
		return nil
	}
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && isPrivateIP(ip) {
			return ErrSSRF
		}
	}
	return nil
}

// ValidateIdentifier rejects identifiers that contain characters unsuitable
// for SQL identifiers, file names, or URL path segments. Allows alphanumeric,
// underscore, hyphen, and dot.
func ValidateIdentifier(s string) error {
	if s == "" {
		return fmt.Errorf("horosafe: identifier must not be empty")
	}
	if len(s) > 256 {
		return fmt.Errorf("horosafe: identifier too long (max 256)")
	}
	for _, r := range s {
		if !isIdentChar(r) {
			return fmt.Errorf("horosafe: invalid character %q in identifier", r)
		}
	}
	return nil
}

// LimitedReadAll reads at most maxBytes from r. Returns ErrResponseTooLarge
// if the limit is exceeded.
func LimitedReadAll(r io.Reader, maxBytes int64) ([]byte, error) {
	lr := io.LimitReader(r, maxBytes+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("horosafe: response exceeds %d bytes", maxBytes)
	}
	return data, nil
}

func isIdentChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.'
}

func isPrivateIP(ip net.IP) bool {
	// Loopback
	if ip.IsLoopback() {
		return true
	}
	// Link-local
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	// RFC 1918 / RFC 4193
	privateRanges := []struct {
		network string
	}{
		{"10.0.0.0/8"},
		{"172.16.0.0/12"},
		{"192.168.0.0/16"},
		{"fc00::/7"},
		{"169.254.0.0/16"},
		{"::1/128"},
	}
	for _, pr := range privateRanges {
		_, cidr, err := net.ParseCIDR(pr.network)
		if err != nil {
			continue
		}
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

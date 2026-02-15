// Package idgen provides pluggable ID generation for the HOROS ecosystem.
//
// All constructors across pkg/ (audit, mcprt, mcpquic) accept a Generator,
// making the ID strategy a startup-time decision rather than a compile-time one.
package idgen

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
)

// Generator produces unique string identifiers.
type Generator func() string

// NanoID returns a Generator that produces base-36 IDs of the given length.
// This is the lightweight strategy — short, URL-safe, fast.
func NanoID(length int) Generator {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	alphabetLen := big.NewInt(int64(len(alphabet)))
	return func() string {
		b := make([]byte, length)
		for i := range b {
			n, _ := rand.Int(rand.Reader, alphabetLen)
			b[i] = alphabet[n.Int64()]
		}
		return string(b)
	}
}

// UUIDv7 returns a Generator that produces RFC 9562 UUID v7 strings.
// Time-sortable, globally unique, ecosystem convention per CLAUDE.md.
func UUIDv7() Generator {
	return func() string {
		return uuid.Must(uuid.NewV7()).String()
	}
}

// Prefixed wraps a Generator and prepends a fixed prefix to every ID.
// Useful for type-scoped identifiers (e.g. "aud_", "sess_", "trc_").
func Prefixed(prefix string, gen Generator) Generator {
	return func() string {
		return prefix + gen()
	}
}

// Timestamped returns a Generator that produces IDs in the format
// "20060102T150405Z_<suffix>" where suffix comes from the inner generator.
func Timestamped(gen Generator) Generator {
	return func() string {
		return time.Now().UTC().Format("20060102T150405Z") + "_" + gen()
	}
}

// Default is the ecosystem default: 12-character NanoID base-36.
// Matches the original horostracker internal/db.NewID() behavior.
var Default Generator = NanoID(12)

// New produces an ID using the Default generator.
func New() string {
	return Default()
}

// MustParse validates a UUID string and returns it or panics.
func MustParse(s string) string {
	_ = uuid.MustParse(s)
	return s
}

// Parse validates a UUID string and returns it or an error.
func Parse(s string) (string, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return "", fmt.Errorf("invalid UUID: %w", err)
	}
	return u.String(), nil
}

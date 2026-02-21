package idgen

import (
	"strings"
	"testing"
)

func TestNanoID_Length(t *testing.T) {
	for _, length := range []int{8, 12, 16, 24} {
		gen := NanoID(length)
		id := gen()
		if len(id) != length {
			t.Fatalf("NanoID(%d): got length %d", length, len(id))
		}
	}
}

func TestNanoID_Alphabet(t *testing.T) {
	gen := NanoID(100)
	id := gen()
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z')) {
			t.Fatalf("NanoID: unexpected character %q in %q", c, id)
		}
	}
}

func TestNanoID_Uniqueness(t *testing.T) {
	gen := NanoID(12)
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		id := gen()
		if _, ok := seen[id]; ok {
			t.Fatalf("NanoID: duplicate at iteration %d: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestUUIDv7_Format(t *testing.T) {
	gen := UUIDv7()
	id := gen()
	// UUID format: 8-4-4-4-12
	parts := strings.Split(id, "-")
	if len(parts) != 5 {
		t.Fatalf("UUIDv7: expected 5 parts, got %d in %q", len(parts), id)
	}
	if len(id) != 36 {
		t.Fatalf("UUIDv7: expected length 36, got %d", len(id))
	}
}

func TestUUIDv7_Uniqueness(t *testing.T) {
	gen := UUIDv7()
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		id := gen()
		if _, ok := seen[id]; ok {
			t.Fatalf("UUIDv7: duplicate at iteration %d", i)
		}
		seen[id] = struct{}{}
	}
}

func TestPrefixed(t *testing.T) {
	gen := Prefixed("aud_", NanoID(8))
	id := gen()
	if !strings.HasPrefix(id, "aud_") {
		t.Fatalf("Prefixed: expected prefix 'aud_', got %q", id)
	}
	if len(id) != 4+8 {
		t.Fatalf("Prefixed: expected length 12, got %d", len(id))
	}
}

func TestTimestamped(t *testing.T) {
	gen := Timestamped(NanoID(6))
	id := gen()
	// Format: 20060102T150405Z_xxxxxx → at least 16+1+6 = 23 chars
	if !strings.Contains(id, "T") || !strings.Contains(id, "Z_") {
		t.Fatalf("Timestamped: bad format %q", id)
	}
}

func TestDefault_IsUUIDv7(t *testing.T) {
	id := New()
	// UUIDv7 format: 8-4-4-4-12 = 36 chars
	if len(id) != 36 {
		t.Fatalf("New (UUIDv7 default): expected length 36, got %d for %q", len(id), id)
	}
	// Must be a valid UUID
	if _, err := Parse(id); err != nil {
		t.Fatalf("New: default should produce valid UUIDv7: %v", err)
	}
}

func TestParse_Valid(t *testing.T) {
	gen := UUIDv7()
	original := gen()
	parsed, err := Parse(original)
	if err != nil {
		t.Fatalf("Parse valid UUID: %v", err)
	}
	if parsed != original {
		t.Fatalf("Parse: got %q, want %q", parsed, original)
	}
}

func TestParse_Invalid(t *testing.T) {
	_, err := Parse("not-a-uuid")
	if err == nil {
		t.Fatal("Parse: expected error for invalid UUID")
	}
}

func TestMustParse_Valid(t *testing.T) {
	gen := UUIDv7()
	original := gen()
	result := MustParse(original)
	if result != original {
		t.Fatalf("MustParse: got %q, want %q", result, original)
	}
}

func TestMustParse_Invalid(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustParse: expected panic for invalid UUID")
		}
	}()
	MustParse("not-a-uuid")
}

package sas_ingester

import (
	"os"
	"testing"
)

func TestExtractMetadata_Text(t *testing.T) {
	f, err := os.CreateTemp("", "meta_test_*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("Hello, this is a plain text file for testing metadata extraction.")
	f.Close()

	meta, err := ExtractMetadata(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if meta.MIME != "text/plain; charset=utf-8" {
		t.Errorf("MIME = %q, want text/plain; charset=utf-8", meta.MIME)
	}
	if !meta.IsText {
		t.Error("expected IsText=true")
	}
	if meta.IsBinary {
		t.Error("expected IsBinary=false")
	}
}

func TestExtractMetadata_Binary(t *testing.T) {
	f, err := os.CreateTemp("", "meta_test_*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	// Write PNG header.
	f.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	f.Write(make([]byte, 100))
	f.Close()

	meta, err := ExtractMetadata(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if meta.MagicHeader != "PNG" {
		t.Errorf("MagicHeader = %q, want PNG", meta.MagicHeader)
	}
	if meta.IsText {
		t.Error("expected IsText=false for PNG")
	}
}

func TestShannonEntropy(t *testing.T) {
	// All same bytes: entropy = 0.
	data := make([]byte, 100)
	e := shannonEntropy(data)
	if e != 0 {
		t.Errorf("entropy of uniform data = %f, want 0", e)
	}

	// Two equally distributed bytes: entropy ≈ 1.
	data2 := make([]byte, 100)
	for i := range data2 {
		data2[i] = byte(i % 2)
	}
	e2 := shannonEntropy(data2)
	if e2 < 0.9 || e2 > 1.1 {
		t.Errorf("entropy of 2-symbol data = %f, want ~1.0", e2)
	}
}

func TestMetadataJSON(t *testing.T) {
	m := &FileMetadata{MIME: "text/plain", Entropy: 3.5, IsText: true}
	j := MetadataJSON(m)
	if j == "{}" || j == "" {
		t.Error("expected non-empty JSON")
	}
}

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

func TestExtractTrailer_PDF(t *testing.T) {
	f, err := os.CreateTemp("", "trailer_pdf_*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	// Write a minimal PDF-like tail.
	f.WriteString("some content\n")
	f.WriteString("startxref\n")
	f.WriteString("12345\n")
	f.WriteString("%%EOF\n")
	f.Close()

	trailer, err := extractTrailer(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !trailer.HasPDFEOF {
		t.Error("expected HasPDFEOF=true")
	}
	if trailer.PDFStartXRef != "12345" {
		t.Errorf("PDFStartXRef = %q, want 12345", trailer.PDFStartXRef)
	}
	if trailer.HasZIPEOCD {
		t.Error("expected HasZIPEOCD=false for PDF")
	}
}

func TestExtractTrailer_ZIP(t *testing.T) {
	f, err := os.CreateTemp("", "trailer_zip_*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())

	// Write minimal ZIP EOCD record (22 bytes minimum, no comment).
	eocd := make([]byte, 22)
	eocd[0] = 0x50 // P
	eocd[1] = 0x4b // K
	eocd[2] = 0x05
	eocd[3] = 0x06
	// Remaining fields are zero (no comment, zero entries).
	f.Write([]byte("some zip data before EOCD\n"))
	f.Write(eocd)
	f.Close()

	trailer, err := extractTrailer(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !trailer.HasZIPEOCD {
		t.Error("expected HasZIPEOCD=true")
	}
	if trailer.HasPDFEOF {
		t.Error("expected HasPDFEOF=false for ZIP")
	}
}

func TestExtractFullMetadata(t *testing.T) {
	dir := t.TempDir()

	// Write two "chunks".
	os.WriteFile(dir+"/chunk_00000.bin", []byte("Hello, this is a plain text file for multi-chunk entropy."), 0644)
	os.WriteFile(dir+"/chunk_00001.bin", []byte("Second chunk with more text content and %%EOF marker."), 0644)

	meta, err := ExtractFullMetadata(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if meta.MIME != "text/plain; charset=utf-8" {
		t.Errorf("MIME = %q", meta.MIME)
	}
	if meta.Entropy <= 0 {
		t.Errorf("Entropy should be > 0, got %f", meta.Entropy)
	}
	if meta.Trailer == nil {
		t.Error("expected trailer info")
	}
}

func TestExtractFullMetadata_ZeroChunks(t *testing.T) {
	meta, err := ExtractFullMetadata("/nonexistent", 0)
	if err != nil {
		t.Fatal(err)
	}
	if meta.MIME != "application/octet-stream" {
		t.Errorf("MIME = %q, want application/octet-stream", meta.MIME)
	}
}

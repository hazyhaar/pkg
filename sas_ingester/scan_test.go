package sas_ingester

import (
	"os"
	"testing"
)

func TestCheckZipBomb(t *testing.T) {
	// Normal data: no alert.
	normal := make([]byte, 1000)
	if w := checkZipBomb(normal, 1000); w != "" {
		t.Errorf("unexpected warning for normal data: %s", w)
	}

	// Suspicious: many PK headers in tiny header, small file.
	suspicious := make([]byte, 0, 2000)
	for i := 0; i < 150; i++ {
		suspicious = append(suspicious, 'P', 'K', 3, 4, 0, 0, 0, 0)
	}
	if w := checkZipBomb(suspicious, int64(len(suspicious))); w == "" {
		t.Error("expected zip bomb warning")
	}

	// Same header but large file → not suspicious (legitimate Office doc).
	if w := checkZipBomb(suspicious, 50*1024*1024); w != "" {
		t.Errorf("large file should not trigger zip bomb: %s", w)
	}
}

func TestCheckPolyglot(t *testing.T) {
	// Single format: no alert.
	pdf := append([]byte("%PDF-1.4"), make([]byte, 100)...)
	if w := checkPolyglot(pdf); w != "" {
		t.Errorf("unexpected polyglot warning for PDF: %s", w)
	}

	// PDF + ELF header: polyglot.
	polyglot := append([]byte("\x7fELF"), make([]byte, 100)...)
	copy(polyglot[10:], []byte("%PDF"))
	if w := checkPolyglot(polyglot); w == "" {
		t.Error("expected polyglot warning")
	}
}

func TestCheckMacro(t *testing.T) {
	// .xlsm extension triggers warning.
	if w := checkMacro([]byte("data"), "report.xlsm"); w == "" {
		t.Error("expected macro warning for .xlsm")
	}

	// Normal .txt: no warning.
	if w := checkMacro([]byte("data"), "doc.txt"); w != "" {
		t.Errorf("unexpected macro warning: %s", w)
	}
}

func TestScanFile(t *testing.T) {
	f, err := os.CreateTemp("", "scan_test_*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("just a plain text file")
	f.Close()

	cfg := DefaultConfig()
	result, err := ScanFile(f.Name(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if result.Blocked {
		t.Error("plain text should not be blocked")
	}
	if result.ClamAV != "skipped" {
		t.Errorf("ClamAV = %q, want skipped (not enabled)", result.ClamAV)
	}
}

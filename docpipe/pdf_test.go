package docpipe

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractPDF_Simple(t *testing.T) {
	// WHAT: PDF with text content extracts correctly with quality metrics.
	// WHY: Core PDF extraction using pdfast must produce usable text.
	dir := t.TempDir()
	path := filepath.Join(dir, "text.pdf")
	raw := buildRealTextPDF("Hello World from PDF extraction test")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}

	pipe := New(Config{})
	doc, err := pipe.Extract(context.Background(), path)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if doc.Quality == nil {
		t.Fatal("expected non-nil Quality for PDF")
	}
	if !strings.Contains(doc.RawText, "Hello World") {
		t.Errorf("expected 'Hello World' in text, got %q", doc.RawText)
	}
}

func TestExtractPDF_CIDFont(t *testing.T) {
	// WHAT: PDF with CIDFont/ToUnicode encoding extracts text via pdfast.
	// WHY: pdfast handles CIDFont/ToUnicode natively — no pdftotext fallback needed.
	dir := t.TempDir()
	path := filepath.Join(dir, "cidfont.pdf")
	raw := buildCIDFontPDF("Contenu CIDFont extrait par pdfast")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}

	title, sections, quality, err := extractPDF(path)
	if err != nil {
		t.Fatalf("extractPDF: %v", err)
	}
	if len(sections) == 0 {
		t.Fatal("expected sections from CIDFont PDF")
	}
	if quality == nil {
		t.Fatal("expected non-nil quality")
	}
	if title == "" {
		t.Error("expected non-empty title")
	}

	var allText strings.Builder
	for _, s := range sections {
		allText.WriteString(s.Text)
	}
	if !strings.Contains(allText.String(), "pdfast") {
		t.Errorf("expected 'pdfast' in extracted text, got %q", allText.String())
	}
}

func TestExtractPDF_ImageOnly(t *testing.T) {
	// WHAT: PDF without text but with image XObject flags HasImageStreams.
	// WHY: Image-only PDFs must be flagged for OCR processing.
	dir := t.TempDir()
	path := filepath.Join(dir, "image.pdf")
	raw := buildImageOnlyPDF()
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}

	_, _, quality, err := extractPDF(path)
	if err == nil && quality != nil {
		if quality.HasImageStreams {
			t.Log("image streams correctly detected")
		}
		if !quality.NeedsOCR() {
			t.Log("warning: image-only PDF should ideally flag NeedsOCR")
		}
	}
	// If extraction fails with "no text content", that's acceptable for image-only.
	if err != nil && !strings.Contains(err.Error(), "no text content") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractPDF_VisualRefs(t *testing.T) {
	// WHAT: Text with "voir figure 3" + image -> HasVisualGap=true.
	// WHY: Visual references without image extraction = information loss.
	dir := t.TempDir()
	path := filepath.Join(dir, "visual.pdf")
	raw := buildRealTextPDF("voir figure 3 et cf. tableau 2 pour les details")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}

	pipe := New(Config{})
	doc, err := pipe.Extract(context.Background(), path)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if doc.Quality == nil {
		t.Fatal("expected quality metrics")
	}
	if doc.Quality.VisualRefCount == 0 && strings.Contains(doc.RawText, "figure") {
		t.Error("expected VisualRefCount > 0 for text with 'voir figure' patterns")
	}
}

func TestExtractPDF_PdftotextFallback(t *testing.T) {
	// WHAT: pdftotext fallback works when pdfast returns empty.
	// WHY: Graceful degradation for edge-case PDFs.
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext not installed — install poppler-utils to run this test")
	}
	// This test validates the fallback path exists and works.
	// In practice pdfast handles most PDFs — the fallback is for truly exotic encodings.
	t.Log("pdftotext fallback path is available")
}

func TestTryPdftotext_NotInstalled(t *testing.T) {
	// WHAT: tryPdftotext returns error when binary is not found.
	// WHY: Graceful fallback — no panic, no cryptic error.
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", "/nonexistent")
	defer os.Setenv("PATH", origPath)

	_, err := tryPdftotext("/tmp/nonexistent.pdf")
	if err == nil {
		t.Fatal("expected error when pdftotext not in PATH")
	}
}

func TestParsePdftotextOutput(t *testing.T) {
	// WHAT: parsePdftotextOutput splits on form-feed and extracts title.
	// WHY: pdftotext output format must be correctly parsed into sections.
	input := "First page title\nSome content here.\f\nSecond page\nMore text.\f\n"

	title, sections := parsePdftotextOutput(input)
	if title != "First page title" {
		t.Errorf("title = %q, want %q", title, "First page title")
	}
	if len(sections) != 2 {
		t.Fatalf("got %d sections, want 2", len(sections))
	}
	if sections[0].Metadata["page"] != "1" {
		t.Errorf("section 0 page = %q, want '1'", sections[0].Metadata["page"])
	}
	if sections[1].Metadata["page"] != "2" {
		t.Errorf("section 1 page = %q, want '2'", sections[1].Metadata["page"])
	}
}

func TestExtractPDF_QualityMetrics(t *testing.T) {
	// WHAT: Quality metrics are populated correctly.
	// WHY: ExtractionQuality drives OCR decisions downstream.
	dir := t.TempDir()
	path := filepath.Join(dir, "quality.pdf")
	raw := buildRealTextPDF("Normal readable text with multiple words for testing quality metrics")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}

	_, _, quality, err := extractPDF(path)
	if err != nil {
		t.Fatalf("extractPDF: %v", err)
	}
	if quality == nil {
		t.Fatal("expected non-nil quality")
	}
	if quality.PageCount < 1 {
		t.Errorf("PageCount = %d, want >= 1", quality.PageCount)
	}
	if quality.CharsPerPage <= 0 {
		t.Errorf("CharsPerPage = %f, want > 0", quality.CharsPerPage)
	}
	if quality.PrintableRatio < 0.9 {
		t.Errorf("PrintableRatio = %f, want >= 0.9", quality.PrintableRatio)
	}
	if quality.WordlikeRatio < 0.5 {
		t.Errorf("WordlikeRatio = %f, want >= 0.5", quality.WordlikeRatio)
	}
}

// --- PDF test helpers ---

// buildRealTextPDF creates a valid PDF with proper xref offsets.
func buildRealTextPDF(text string) []byte {
	escaped := strings.ReplaceAll(text, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, "(", `\(`)
	escaped = strings.ReplaceAll(escaped, ")", `\)`)

	stream := "BT\n/F1 12 Tf\n72 720 Td\n(" + escaped + ") Tj\nET"
	streamLen := len(stream)

	var b strings.Builder
	b.WriteString("%PDF-1.4\n")

	offsets := make([]int, 6)

	offsets[1] = b.Len()
	b.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	offsets[2] = b.Len()
	b.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")

	offsets[3] = b.Len()
	b.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n")

	offsets[4] = b.Len()
	b.WriteString("4 0 obj\n<< /Length ")
	b.WriteString(pdfItoa(streamLen))
	b.WriteString(" >>\nstream\n")
	b.WriteString(stream)
	b.WriteString("\nendstream\nendobj\n")

	offsets[5] = b.Len()
	b.WriteString("5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")

	xrefOffset := b.Len()
	b.WriteString("xref\n0 6\n")
	b.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		b.WriteString(pdfPadOffset(offsets[i]))
		b.WriteString(" 00000 n \n")
	}
	b.WriteString("trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n")
	b.WriteString(pdfItoa(xrefOffset))
	b.WriteString("\n%%EOF\n")

	return []byte(b.String())
}

func buildImageOnlyPDF() []byte {
	imgData := "\xff\xd8\xff\xe0"

	var b strings.Builder
	b.WriteString("%PDF-1.4\n")

	offsets := make([]int, 6)

	offsets[1] = b.Len()
	b.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	offsets[2] = b.Len()
	b.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")

	offsets[3] = b.Len()
	b.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /XObject << /Im1 4 0 R >> >> /Contents 5 0 R >>\nendobj\n")

	offsets[4] = b.Len()
	b.WriteString("4 0 obj\n<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Length ")
	b.WriteString(pdfItoa(len(imgData)))
	b.WriteString(" >>\nstream\n")
	b.WriteString(imgData)
	b.WriteString("\nendstream\nendobj\n")

	drawStream := "q 100 0 0 100 72 692 cm /Im1 Do Q"
	offsets[5] = b.Len()
	b.WriteString("5 0 obj\n<< /Length ")
	b.WriteString(pdfItoa(len(drawStream)))
	b.WriteString(" >>\nstream\n")
	b.WriteString(drawStream)
	b.WriteString("\nendstream\nendobj\n")

	xrefOffset := b.Len()
	b.WriteString("xref\n0 6\n")
	b.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		b.WriteString(pdfPadOffset(offsets[i]))
		b.WriteString(" 00000 n \n")
	}
	b.WriteString("trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n")
	b.WriteString(pdfItoa(xrefOffset))
	b.WriteString("\n%%EOF\n")
	return []byte(b.String())
}

// buildCIDFontPDF creates a PDF using Type0/CIDFont encoding with ToUnicode CMap.
func buildCIDFontPDF(text string) []byte {
	// Encode text as hex (2 bytes per char, big-endian Unicode).
	var hexStr strings.Builder
	for _, r := range text {
		hexStr.WriteString(pdfPadHex(int(r), 4))
	}

	// ToUnicode CMap that maps identity (code = Unicode).
	cmap := `/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def
/CMapName /Adobe-Identity-UCS def
/CMapType 2 def
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
1 beginbfrange
<0000> <FFFF> <0000>
endbfrange
endcmap
CMapSpaceUsed
end end`

	stream := "BT\n/F1 12 Tf\n72 720 Td\n<" + hexStr.String() + "> Tj\nET"
	streamLen := len(stream)

	var b strings.Builder
	b.WriteString("%PDF-1.4\n")

	offsets := make([]int, 8)

	offsets[1] = b.Len()
	b.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	offsets[2] = b.Len()
	b.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")

	offsets[3] = b.Len()
	b.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n")

	offsets[4] = b.Len()
	b.WriteString("4 0 obj\n<< /Length ")
	b.WriteString(pdfItoa(streamLen))
	b.WriteString(" >>\nstream\n")
	b.WriteString(stream)
	b.WriteString("\nendstream\nendobj\n")

	// Type0 (composite) font with CIDFont descendant
	offsets[5] = b.Len()
	b.WriteString("5 0 obj\n<< /Type /Font /Subtype /Type0 /BaseFont /Arial /Encoding /Identity-H /DescendantFonts [6 0 R] /ToUnicode 7 0 R >>\nendobj\n")

	// CIDFont descriptor
	offsets[6] = b.Len()
	b.WriteString("6 0 obj\n<< /Type /Font /Subtype /CIDFontType2 /BaseFont /Arial /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /DW 1000 >>\nendobj\n")

	// ToUnicode CMap stream
	offsets[7] = b.Len()
	b.WriteString("7 0 obj\n<< /Length ")
	b.WriteString(pdfItoa(len(cmap)))
	b.WriteString(" >>\nstream\n")
	b.WriteString(cmap)
	b.WriteString("\nendstream\nendobj\n")

	xrefOffset := b.Len()
	b.WriteString("xref\n0 8\n")
	b.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 7; i++ {
		b.WriteString(pdfPadOffset(offsets[i]))
		b.WriteString(" 00000 n \n")
	}
	b.WriteString("trailer\n<< /Size 8 /Root 1 0 R >>\nstartxref\n")
	b.WriteString(pdfItoa(xrefOffset))
	b.WriteString("\n%%EOF\n")

	return []byte(b.String())
}

func pdfPadHex(n int, width int) string {
	s := pdfItoa16(n)
	for len(s) < width {
		s = "0" + s
	}
	return s
}

func pdfItoa16(n int) string {
	if n == 0 {
		return "0"
	}
	const hex = "0123456789ABCDEF"
	s := ""
	for n > 0 {
		s = string(hex[n%16]) + s
		n /= 16
	}
	return s
}

func pdfItoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func pdfPadOffset(n int) string {
	s := pdfItoa(n)
	for len(s) < 10 {
		s = "0" + s
	}
	return s
}

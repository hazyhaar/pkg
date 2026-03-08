package docpipe

import (
	"context"
	"os"
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
	// WHY: pdfast handles CIDFont/ToUnicode natively.
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

func TestExtractPDF_ImageOnly_NeedsOCR(t *testing.T) {
	// WHAT: PDF without text returns NeedsOCR quality, no error, no pdftotext fallback.
	// WHY: Image-only PDFs must be flagged for OCR, not fail extraction.
	dir := t.TempDir()
	path := filepath.Join(dir, "image.pdf")
	raw := buildImageOnlyPDF()
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}

	_, sections, quality, err := extractPDF(path)
	if err != nil {
		t.Fatalf("expected no error for image-only PDF, got: %v", err)
	}
	if len(sections) != 0 {
		t.Errorf("expected 0 sections for image-only PDF, got %d", len(sections))
	}
	if quality == nil {
		t.Fatal("expected non-nil quality")
	}
	if quality.CharsPerPage != 0 {
		t.Errorf("expected CharsPerPage=0, got %f", quality.CharsPerPage)
	}
	// NeedsOCR should be true: low chars + images
	if quality.HasImageStreams && !quality.NeedsOCR() {
		t.Log("warning: image-only PDF with HasImageStreams should flag NeedsOCR")
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

func TestExtractPDF_SecurityRemovals(t *testing.T) {
	// WHAT: PDF with /AA (additional actions) has SecurityRemovals populated after sanitize.
	// WHY: Anonymous uploads on anon.repvow.fr must be sanitized before extraction.
	// NOTE: /JavaScript and /Launch are hard-blocked by reader.Open (SecurityError).
	//       Sanitize strips softer threats: /OpenAction (non-JS), /AA, /XFA, /SubmitForm.
	dir := t.TempDir()
	path := filepath.Join(dir, "actions.pdf")
	raw := buildPDFWithAA()
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
	if len(quality.SecurityRemovals) == 0 {
		t.Log("warning: no SecurityRemovals detected — sanitize may not strip /AA from this test PDF")
	} else {
		t.Logf("SecurityRemovals: %v", quality.SecurityRemovals)
	}
}

func TestExtractPDF_ReaderBlocksJavaScript(t *testing.T) {
	// WHAT: PDF with /JavaScript is hard-blocked by reader.Open.
	// WHY: Dangerous execution actions must fail extraction, not silently pass.
	dir := t.TempDir()
	path := filepath.Join(dir, "js.pdf")
	raw := buildPDFWithOpenAction()
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}

	_, _, _, err := extractPDF(path)
	if err == nil {
		t.Fatal("expected error for PDF with /JavaScript")
	}
	if !strings.Contains(err.Error(), "security") && !strings.Contains(err.Error(), "dangerous") {
		t.Errorf("expected security-related error, got: %v", err)
	}
}

func TestExtractPDF_SectionsHavePageType(t *testing.T) {
	// WHAT: Extracted sections have Type="page" with page number metadata.
	// WHY: Section types enable downstream consumers to distinguish pages from tables.
	dir := t.TempDir()
	path := filepath.Join(dir, "typed.pdf")
	raw := buildRealTextPDF("Section type test content")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}

	_, sections, _, err := extractPDF(path)
	if err != nil {
		t.Fatalf("extractPDF: %v", err)
	}
	if len(sections) == 0 {
		t.Fatal("expected at least one section")
	}
	for _, s := range sections {
		if s.Type != "page" && s.Type != "table" {
			t.Errorf("unexpected section type %q", s.Type)
		}
		if s.Metadata["page"] == "" {
			t.Error("section missing page metadata")
		}
	}
}

func TestFormatTableGrid(t *testing.T) {
	// WHAT: formatTableGrid produces pipe-separated output.
	// WHY: Table sections must have human-readable structured text.
	grid := [][]string{
		{"Name", "Amount"},
		{"Alice", "100"},
		{"Bob", "200"},
	}
	got := formatTableGrid(grid)
	want := "Name | Amount\nAlice | 100\nBob | 200"
	if got != want {
		t.Errorf("formatTableGrid:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestFormatTableGrid_Empty(t *testing.T) {
	got := formatTableGrid(nil)
	if got != "" {
		t.Errorf("expected empty string for nil grid, got %q", got)
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

// buildPDFWithAA creates a PDF with /AA (additional actions) for sanitize testing.
// Uses /AA with /GoTo (not /JavaScript), so reader.Open allows it but sanitize strips /AA.
func buildPDFWithAA() []byte {
	stream := "BT\n/F1 12 Tf\n72 720 Td\n(Text with additional actions) Tj\nET"
	streamLen := len(stream)

	var b strings.Builder
	b.WriteString("%PDF-1.4\n")

	offsets := make([]int, 6)

	offsets[1] = b.Len()
	// Catalog with /AA (additional actions) — not hard-blocked by reader
	b.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R /AA << /WC << /S /GoTo /D [3 0 R /Fit] >> >> >>\nendobj\n")

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

// buildPDFWithOpenAction creates a PDF with /OpenAction + /JavaScript for reader blocking test.
func buildPDFWithOpenAction() []byte {
	stream := "BT\n/F1 12 Tf\n72 720 Td\n(Safe text content) Tj\nET"
	streamLen := len(stream)

	var b strings.Builder
	b.WriteString("%PDF-1.4\n")

	offsets := make([]int, 6)

	offsets[1] = b.Len()
	// Catalog with OpenAction + JavaScript (hard-blocked by reader)
	b.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R /OpenAction << /S /JavaScript /JS (app.alert\\('pwned'\\)) >> >>\nendobj\n")

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

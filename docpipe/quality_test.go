package docpipe

import "testing"

func TestPrintableRatio_Normal(t *testing.T) {
	// WHAT: Normal text has high printable ratio.
	// WHY: Validates baseline quality scoring.
	ratio := computePrintableRatio("This is a normal sentence with standard characters.")
	if ratio < 0.95 {
		t.Errorf("printable ratio = %f, want > 0.95", ratio)
	}
}

func TestPrintableRatio_Garbage(t *testing.T) {
	// WHAT: PUA and control chars produce low printable ratio.
	// WHY: Detects garbled PDF extraction (CIDFont without ToUnicode).
	garbage := "abc\uE000\uE001\uE002\uE003\uE004def\uE005\uE006\uE007\uE008\uE009ghi\x01\x02\x03\x04\x05"
	ratio := computePrintableRatio(garbage)
	if ratio >= 0.85 {
		t.Errorf("printable ratio = %f, want < 0.85", ratio)
	}
}

func TestWordlikeRatio_Normal(t *testing.T) {
	// WHAT: Normal phrases have high wordlike ratio.
	// WHY: Real text has multi-character words.
	ratio := computeWordlikeRatio("This is a normal sentence with standard words inside")
	if ratio < 0.70 {
		t.Errorf("wordlike ratio = %f, want > 0.70", ratio)
	}
}

func TestWordlikeRatio_SingleChar(t *testing.T) {
	// WHAT: Single-char tokens produce low wordlike ratio.
	// WHY: Detects broken character-by-character extraction.
	ratio := computeWordlikeRatio("a b c d e f g h i j k l")
	if ratio >= 0.40 {
		t.Errorf("wordlike ratio = %f, want < 0.40", ratio)
	}
}

func TestCountVisualRefs(t *testing.T) {
	// WHAT: Visual reference patterns are counted.
	// WHY: Detects references to figures/tables that may need image extraction.
	text := "voir figure 3, cf. tableau 2, see Figure 1"
	count := countVisualRefs(text)
	// Both patterns can match: pattern 1 matches "voir figure 3", "cf. tableau 2", "see Figure 1"
	// Pattern 2 also matches "figure 3", "tableau 2", "Figure 1" independently.
	if count < 3 {
		t.Errorf("visual refs = %d, want >= 3", count)
	}
}

func TestNeedsOCR(t *testing.T) {
	// WHAT: Low chars per page + images = needs OCR.
	// WHY: Image-only PDFs need OCR flagging.
	q := &ExtractionQuality{
		CharsPerPage:    30,
		HasImageStreams: true,
		PrintableRatio:  0.9,
	}
	if !q.NeedsOCR() {
		t.Error("expected NeedsOCR=true for low chars + images")
	}
}

func TestHasVisualGap(t *testing.T) {
	// WHAT: Visual refs + images = visual gap.
	// WHY: Text references figures but we only extract text.
	q := &ExtractionQuality{
		VisualRefCount:  2,
		HasImageStreams: true,
	}
	if !q.HasVisualGap() {
		t.Error("expected HasVisualGap=true for visual refs + images")
	}
}

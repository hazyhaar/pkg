// CLAUDE:SUMMARY Scoring qualite d'extraction PDF — detecte besoins OCR et lacunes visuelles.
// CLAUDE:EXPORTS ExtractionQuality, NeedsOCR, HasVisualGap, computePrintableRatio, computeWordlikeRatio, countVisualRefs
package docpipe

import (
	"regexp"
	"strings"
	"unicode"
)

// ExtractionQuality captures metrics about PDF text extraction quality.
type ExtractionQuality struct {
	PageCount      int     `json:"page_count"`
	CharsPerPage   float64 `json:"chars_per_page"`
	PrintableRatio float64 `json:"printable_ratio"`
	WordlikeRatio  float64 `json:"wordlike_ratio"`
	HasImageStreams bool    `json:"has_image_streams"`
	VisualRefCount int     `json:"visual_ref_count"`
}

// NeedsOCR returns true if the PDF likely needs OCR to extract text.
func (q *ExtractionQuality) NeedsOCR() bool {
	return (q.CharsPerPage < 50 && q.HasImageStreams) || q.PrintableRatio < 0.85
}

// HasVisualGap returns true if the text references figures/tables but the PDF has images.
func (q *ExtractionQuality) HasVisualGap() bool {
	return q.VisualRefCount > 0 && q.HasImageStreams
}

// computePrintableRatio returns the ratio of printable characters in text.
// Excludes PUA U+E000-U+F8FF, control chars < U+0020 (except \n\r\t), U+FFFD.
func computePrintableRatio(text string) float64 {
	if len(text) == 0 {
		return 1.0
	}
	total := 0
	printable := 0
	for _, r := range text {
		total++
		if isGarbageRune(r) {
			continue
		}
		if unicode.IsPrint(r) || r == '\n' || r == '\r' || r == '\t' {
			printable++
		}
	}
	if total == 0 {
		return 1.0
	}
	return float64(printable) / float64(total)
}

func isGarbageRune(r rune) bool {
	// Private Use Area
	if r >= 0xE000 && r <= 0xF8FF {
		return true
	}
	// Replacement character
	if r == 0xFFFD {
		return true
	}
	// Control chars except whitespace
	if r < 0x0020 && r != '\n' && r != '\r' && r != '\t' {
		return true
	}
	return false
}

// computeWordlikeRatio returns the ratio of word-like tokens (length 2-15) to total tokens.
func computeWordlikeRatio(text string) float64 {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return 0
	}
	wordlike := 0
	for _, f := range fields {
		n := len([]rune(f))
		if n >= 2 && n <= 15 {
			wordlike++
		}
	}
	return float64(wordlike) / float64(len(fields))
}

var visualRefPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(voir|cf\.?|see|refer\s+to)\s+(la\s+)?(figure|fig\.?|tableau|table|sch[eé]ma|schema|image|illustration|graphique|graph|diagramme|diagram)\s*\d`),
	regexp.MustCompile(`(?i)(figure|fig\.?|tableau|table)\s+\d+`),
}

// countVisualRefs counts references to figures, tables, and diagrams in text.
func countVisualRefs(text string) int {
	count := 0
	for _, pat := range visualRefPatterns {
		matches := pat.FindAllString(text, -1)
		count += len(matches)
	}
	return count
}

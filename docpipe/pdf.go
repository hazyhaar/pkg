// CLAUDE:SUMMARY PDF text extractor using pdfcpu — page-aware extraction with quality scoring.
// CLAUDE:DEPENDS docpipe/quality.go
// CLAUDE:EXPORTS extractPDF
package docpipe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// extractPDF extracts text from a PDF file using pdfcpu for structure-aware parsing.
// Returns title, sections (one per page), extraction quality metrics, and error.
func extractPDF(path string) (string, []Section, *ExtractionQuality, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, nil, err
	}
	defer f.Close()

	conf := model.NewDefaultConfiguration()
	ctx, err := api.ReadValidateAndOptimize(f, conf)
	if err != nil {
		return "", nil, nil, fmt.Errorf("pdfcpu read: %w", err)
	}

	hasImages := detectImageStreams(ctx)

	var sections []Section
	var title string
	totalChars := 0

	for pageNr := 1; pageNr <= ctx.PageCount; pageNr++ {
		pageText := extractPageText(ctx, pageNr)
		if pageText == "" {
			continue
		}

		totalChars += len([]rune(pageText))

		if title == "" {
			// First non-empty line as title.
			for _, line := range strings.Split(pageText, "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					title = line
					if len(title) > 200 {
						title = title[:200]
					}
					break
				}
			}
		}

		sections = append(sections, Section{
			Text: pageText,
			Type: "page",
			Metadata: map[string]string{
				"page": strconv.Itoa(pageNr),
			},
		})

	}

	if len(sections) == 0 {
		// Fallback: try pdftotext (poppler) as subprocess.
		// pdfcpu can't extract text from CIDFont/ToUnicode PDFs (e.g. browser print-to-PDF).
		if text, err := tryPdftotext(path); err == nil && text != "" {
			title, sections = parsePdftotextOutput(text)
			totalChars = len([]rune(text))
		} else {
			return "", nil, nil, fmt.Errorf("no text content found in PDF")
		}
	}

	fullText := strings.Builder{}
	for i, s := range sections {
		if i > 0 {
			fullText.WriteByte('\n')
		}
		fullText.WriteString(s.Text)
	}

	var charsPerPage float64
	if ctx.PageCount > 0 {
		charsPerPage = float64(totalChars) / float64(ctx.PageCount)
	}

	ft := fullText.String()
	quality := &ExtractionQuality{
		PageCount:      ctx.PageCount,
		CharsPerPage:   charsPerPage,
		PrintableRatio: computePrintableRatio(ft),
		WordlikeRatio:  computeWordlikeRatio(ft),
		HasImageStreams: hasImages,
		VisualRefCount: countVisualRefs(ft),
	}

	return title, sections, quality, nil
}

// extractPageText extracts text from a single PDF page via pdfcpu content stream.
func extractPageText(ctx *model.Context, pageNr int) string {
	r, err := pdfcpu.ExtractPageContent(ctx, pageNr)
	if err != nil {
		return ""
	}
	data, err := io.ReadAll(r)
	if err != nil || len(data) == 0 {
		return ""
	}
	return extractTextFromStream(data)
}

// detectImageStreams checks if the PDF contains image XObjects.
func detectImageStreams(ctx *model.Context) bool {
	if ctx.Optimize != nil {
		for pageNr := 1; pageNr <= ctx.PageCount; pageNr++ {
			objNrs := pdfcpu.ImageObjNrs(ctx, pageNr)
			if len(objNrs) > 0 {
				return true
			}
		}
	}
	// Fallback: scan XRefTable for image subtype objects.
	for _, entry := range ctx.Table {
		if entry == nil || entry.Free || entry.Compressed {
			continue
		}
		sd, ok := entry.Object.(types.StreamDict)
		if !ok {
			continue
		}
		if subtype, found := sd.Find("Subtype"); found {
			if name, isName := subtype.(types.Name); isName && name == "Image" {
				return true
			}
		}
	}
	return false
}

// pdfStringRe matches PDF string literals in parentheses: (text here)
var pdfStringRe = regexp.MustCompile(`\(([^)]*)\)`)

// extractTextFromStream parses PDF content stream operators for text.
func extractTextFromStream(data []byte) string {
	var sb strings.Builder

	lines := bytes.Split(data, []byte{'\n'})
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		// Tj operator: (text) Tj
		if bytes.HasSuffix(line, []byte("Tj")) {
			matches := pdfStringRe.FindAllSubmatch(line, -1)
			for _, m := range matches {
				text := decodePDFString(m[1])
				if text != "" {
					sb.WriteString(text)
				}
			}
		}

		// TJ operator: [(text) -100 (more text)] TJ
		if bytes.HasSuffix(line, []byte("TJ")) {
			matches := pdfStringRe.FindAllSubmatch(line, -1)
			for _, m := range matches {
				text := decodePDFString(m[1])
				if text != "" {
					sb.WriteString(text)
				}
			}
		}

		// ' operator (move to next line and show text): (text) '
		if bytes.HasSuffix(line, []byte("'")) && bytes.Contains(line, []byte("(")) {
			matches := pdfStringRe.FindAllSubmatch(line, -1)
			for _, m := range matches {
				text := decodePDFString(m[1])
				if text != "" {
					sb.WriteByte('\n')
					sb.WriteString(text)
				}
			}
		}

		// Td/TD operator (text positioning — add space/newline).
		if bytes.HasSuffix(line, []byte("Td")) || bytes.HasSuffix(line, []byte("TD")) {
			if sb.Len() > 0 {
				sb.WriteByte(' ')
			}
		}

		// T* operator (move to start of next line).
		if bytes.Equal(line, []byte("T*")) {
			sb.WriteByte('\n')
		}
	}

	return cleanPDFText(sb.String())
}

// decodePDFString handles basic PDF escape sequences.
func decodePDFString(raw []byte) string {
	var sb strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' && i+1 < len(raw) {
			i++
			switch raw[i] {
			case 'n':
				sb.WriteByte('\n')
			case 'r':
				sb.WriteByte('\r')
			case 't':
				sb.WriteByte('\t')
			case '\\':
				sb.WriteByte('\\')
			case '(':
				sb.WriteByte('(')
			case ')':
				sb.WriteByte(')')
			default:
				// Octal escape (e.g. \040 for space).
				if raw[i] >= '0' && raw[i] <= '7' {
					val := int(raw[i] - '0')
					if i+1 < len(raw) && raw[i+1] >= '0' && raw[i+1] <= '7' {
						i++
						val = val*8 + int(raw[i]-'0')
						if i+1 < len(raw) && raw[i+1] >= '0' && raw[i+1] <= '7' {
							i++
							val = val*8 + int(raw[i]-'0')
						}
					}
					sb.WriteByte(byte(val))
				} else {
					sb.WriteByte(raw[i])
				}
			}
		} else {
			sb.WriteByte(raw[i])
		}
	}
	return sb.String()
}

// cleanPDFText normalises whitespace in extracted PDF text.
func cleanPDFText(text string) string {
	var sb strings.Builder
	prevSpace := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			if !prevSpace && sb.Len() > 0 {
				sb.WriteByte(' ')
				prevSpace = true
			}
		} else if unicode.IsPrint(r) {
			sb.WriteRune(r)
			prevSpace = false
		}
	}
	return strings.TrimSpace(sb.String())
}

// tryPdftotext attempts to extract text using the pdftotext binary (poppler-utils).
// Returns the extracted text or an error if pdftotext is not installed or fails.
func tryPdftotext(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", path, "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pdftotext: %w: %s", err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// parsePdftotextOutput splits pdftotext output into title + sections.
// Pages are separated by form-feed (\f) characters.
func parsePdftotextOutput(text string) (string, []Section) {
	pages := strings.Split(text, "\f")
	sections := make([]Section, 0, len(pages))
	var title string

	for i, page := range pages {
		page = strings.TrimSpace(page)
		if page == "" {
			continue
		}

		if title == "" {
			for _, line := range strings.Split(page, "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					title = line
					if len(title) > 200 {
						title = title[:200]
					}
					break
				}
			}
		}

		sections = append(sections, Section{
			Text: page,
			Type: "page",
			Metadata: map[string]string{
				"page": strconv.Itoa(i + 1),
			},
		})
	}

	return title, sections
}

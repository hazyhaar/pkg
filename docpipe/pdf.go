// CLAUDE:SUMMARY PDF text extractor using pdfast — page-aware extraction with quality scoring, pdftotext fallback.
// CLAUDE:DEPENDS docpipe/quality.go, pdfast/ops/text, pdfast/pkg/reader
// CLAUDE:EXPORTS extractPDF
// CLAUDE:WARN pdfast remplace pdfcpu pour l'extraction. pdftotext reste fallback ultime si pdfast retourne 0 sections.
package docpipe

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	pdftext "github.com/hazyhaar/pdfast/ops/text"
)

// extractPDF extracts text from a PDF file using pdfast for structure-aware parsing.
// Returns title, sections (one per page), extraction quality metrics, and error.
// Falls back to pdftotext (poppler) if pdfast extracts nothing.
func extractPDF(path string) (string, []Section, *ExtractionQuality, error) {
	// --- pdfast extraction ---
	pages, pdfErr := pdftext.ExtractFile(path)

	var info *pdftext.PDFInfo
	if pdfErr == nil {
		info, _ = pdftext.FileInfo(path)
	}

	var sections []Section
	var title string
	totalChars := 0
	pageCount := 0

	if pdfErr == nil {
		for _, page := range pages {
			text := strings.TrimSpace(page.Text)
			if text == "" {
				continue
			}

			totalChars += len([]rune(text))

			if title == "" {
				for _, line := range strings.Split(text, "\n") {
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
				Text: text,
				Type: "page",
				Metadata: map[string]string{
					"page": strconv.Itoa(page.PageNum),
				},
			})
		}
		pageCount = len(pages)
	}

	if len(sections) == 0 {
		// Fallback: try pdftotext (poppler) as subprocess.
		if text, err := tryPdftotext(path); err == nil && text != "" {
			title, sections = parsePdftotextOutput(text)
			totalChars = len([]rune(text))
		} else if pdfErr != nil {
			return "", nil, nil, fmt.Errorf("pdf extraction failed: %w", pdfErr)
		} else {
			return "", nil, nil, fmt.Errorf("no text content found in PDF")
		}
	}

	// Page count: prefer info (accurate), fallback to extracted pages count
	if info != nil && info.PageCount > 0 {
		pageCount = info.PageCount
	}
	if pageCount == 0 {
		pageCount = len(sections)
	}

	fullText := strings.Builder{}
	for i, s := range sections {
		if i > 0 {
			fullText.WriteByte('\n')
		}
		fullText.WriteString(s.Text)
	}

	var charsPerPage float64
	if pageCount > 0 {
		charsPerPage = float64(totalChars) / float64(pageCount)
	}

	hasImages := false
	if info != nil {
		hasImages = info.HasImageStreams
	}

	ft := fullText.String()
	quality := &ExtractionQuality{
		PageCount:      pageCount,
		CharsPerPage:   charsPerPage,
		PrintableRatio: computePrintableRatio(ft),
		WordlikeRatio:  computeWordlikeRatio(ft),
		HasImageStreams: hasImages,
		VisualRefCount: countVisualRefs(ft),
	}

	return title, sections, quality, nil
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
	rawPages := strings.Split(text, "\f")
	sections := make([]Section, 0, len(rawPages))
	var title string

	for i, page := range rawPages {
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

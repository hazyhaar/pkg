// CLAUDE:SUMMARY PDF text extractor using pdfast — sanitize, page-aware extraction, table detection, layout analysis.
// CLAUDE:DEPENDS docpipe/quality.go, pdfast/ops/text, pdfast/ops/sanitize, pdfast/ops/tables, pdfast/ops/layout, pdfast/pkg/reader
// CLAUDE:EXPORTS extractPDF
package docpipe

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/hazyhaar/pdfast/ops/layout"
	"github.com/hazyhaar/pdfast/ops/sanitize"
	"github.com/hazyhaar/pdfast/ops/tables"
	pdftext "github.com/hazyhaar/pdfast/ops/text"
	"github.com/hazyhaar/pdfast/pkg/reader"
)

// extractPDF extracts text from a PDF file using pdfast with sanitize-first pipeline.
// Pipeline: open → sanitize → extract text → detect tables → detect layout.
// Returns title, sections (pages + tables), extraction quality metrics, and error.
// If pdfast extracts nothing, returns empty document with NeedsOCR quality (no error).
func extractPDF(path string) (string, []Section, *ExtractionQuality, error) {
	// --- Open file ---
	f, err := os.Open(path)
	if err != nil {
		return "", nil, nil, fmt.Errorf("open pdf: %w", err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return "", nil, nil, fmt.Errorf("stat pdf: %w", err)
	}

	// --- Parse via reader.Open (includes structural security scan) ---
	res, err := reader.Open(f, stat.Size())
	if err != nil {
		return "", nil, nil, fmt.Errorf("pdfast open: %w", err)
	}

	// --- Sanitize: strip JS, OpenAction, Launch, XFA before text extraction ---
	var securityRemovals []string
	sanResult, sanErr := sanitize.Sanitize(res.Store)
	if sanErr == nil && sanResult != nil {
		for _, r := range sanResult.Removed {
			securityRemovals = append(securityRemovals, fmt.Sprintf("%s (obj %d: %s)", r.Key, r.ObjectNum, r.Reason))
		}
	}

	// --- Extract text from store ---
	pages, pdfErr := pdftext.Extract(res.Store)

	// --- Get PDF info for image streams detection ---
	var hasImages bool
	info, _ := pdftext.FileInfo(path)
	if info != nil {
		hasImages = info.HasImageStreams
	}

	var sections []Section
	var title string
	totalChars := 0

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

			pageMeta := map[string]string{
				"page": strconv.Itoa(page.PageNum),
			}

			// --- Layout detection per page ---
			if len(page.Blocks) > 0 {
				pl := layout.DetectLayout(page.Blocks, 612, 792)
				if pl.IsMultiCol {
					pageMeta["columns"] = strconv.Itoa(len(pl.Columns))
				}
			}

			sections = append(sections, Section{
				Text:     text,
				Type:     "page",
				Metadata: pageMeta,
			})

			// --- Table detection per page ---
			if len(page.Blocks) >= 4 {
				tbls := tables.Extract(page.Blocks, tables.DefaultConfig())
				for ti, tbl := range tbls {
					grid := tbl.ToStrings()
					tableText := formatTableGrid(grid)
					if tableText != "" {
						sections = append(sections, Section{
							Text: tableText,
							Type: "table",
							Metadata: map[string]string{
								"page":    strconv.Itoa(page.PageNum),
								"table":   strconv.Itoa(ti + 1),
								"rows":    strconv.Itoa(tbl.NumRows),
								"columns": strconv.Itoa(tbl.NumCols),
							},
						})
					}
				}
			}
		}
	}

	// No text extracted: NeedsOCR quality, no error
	if len(sections) == 0 {
		pageCount := 0
		if res.Doc != nil {
			pageCount = res.Doc.PageCount
		}
		quality := &ExtractionQuality{
			PageCount:        pageCount,
			CharsPerPage:     0,
			HasImageStreams:   hasImages,
			SecurityRemovals: securityRemovals,
		}
		return "", nil, quality, nil
	}

	// --- Page count ---
	pageCount := len(pages)
	if res.Doc != nil && res.Doc.PageCount > 0 {
		pageCount = res.Doc.PageCount
	}

	// --- Build full text ---
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

	ft := fullText.String()
	quality := &ExtractionQuality{
		PageCount:        pageCount,
		CharsPerPage:     charsPerPage,
		PrintableRatio:   computePrintableRatio(ft),
		WordlikeRatio:    computeWordlikeRatio(ft),
		HasImageStreams:  hasImages,
		VisualRefCount:   countVisualRefs(ft),
		SecurityRemovals: securityRemovals,
	}

	return title, sections, quality, nil
}

// formatTableGrid formats a 2D string grid into a pipe-separated table.
func formatTableGrid(grid [][]string) string {
	if len(grid) == 0 {
		return ""
	}
	var b strings.Builder
	for i, row := range grid {
		if i > 0 {
			b.WriteByte('\n')
		}
		for j, cell := range row {
			if j > 0 {
				b.WriteString(" | ")
			}
			b.WriteString(strings.TrimSpace(cell))
		}
	}
	return b.String()
}

// CLAUDE:SUMMARY Core pipeline engine that dispatches document extraction by format (docx, odt, pdf, md, txt, html).
// Package docpipe extracts structured text from document files.
//
// Supported formats:
//   - .docx  — Microsoft Word (archive/zip → word/document.xml)
//   - .odt   — OpenDocument Text (archive/zip → content.xml)
//   - .pdf   — PDF text extraction (pure Go, cross-reference + stream decoding)
//   - .md    — Markdown (parsed with heading detection)
//   - .txt   — Plain text (passthrough with whitespace normalization)
//   - .html  — HTML (reuses domkeeper extract pipeline)
//
// All parsers are pure Go, CGO_ENABLED=0 compatible, with zero external dependencies.
//
// Usage:
//
//	pipe := docpipe.New(docpipe.Config{})
//	doc, err := pipe.Extract(ctx, "/path/to/file.docx")
//	fmt.Println(doc.Title, len(doc.Sections), "sections")
package docpipe

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Pipeline is the document extraction engine.
type Pipeline struct {
	cfg    Config
	logger *slog.Logger
}

// New creates a Pipeline with the given configuration.
func New(cfg Config) *Pipeline {
	cfg.defaults()
	return &Pipeline{
		cfg:    cfg,
		logger: cfg.Logger,
	}
}

// Detect returns the document format based on file extension.
func (p *Pipeline) Detect(path string) (Format, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".docx":
		return FormatDocx, nil
	case ".odt":
		return FormatODT, nil
	case ".pdf":
		return FormatPDF, nil
	case ".md", ".markdown":
		return FormatMD, nil
	case ".txt", ".text":
		return FormatTXT, nil
	case ".html", ".htm":
		return FormatHTML, nil
	default:
		return "", fmt.Errorf("unsupported format: %q", ext)
	}
}

// Extract parses a document and returns structured sections.
func (p *Pipeline) Extract(ctx context.Context, path string) (*Document, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Size() > p.cfg.MaxFileSize {
		return nil, fmt.Errorf("file too large: %d bytes (max %d)", info.Size(), p.cfg.MaxFileSize)
	}

	format, err := p.Detect(path)
	if err != nil {
		return nil, err
	}

	p.logger.Debug("extracting document", "path", path, "format", format)

	var sections []Section
	var title string
	var pdfQuality *ExtractionQuality

	switch format {
	case FormatDocx:
		title, sections, err = extractDocx(path)
	case FormatODT:
		title, sections, err = extractODT(path)
	case FormatPDF:
		title, sections, pdfQuality, err = extractPDF(path)
	case FormatMD:
		title, sections, err = extractMarkdown(path)
	case FormatTXT:
		title, sections, err = extractText(path)
	case FormatHTML:
		title, sections, err = extractHTMLFile(path)
	default:
		return nil, fmt.Errorf("no parser for format: %s", format)
	}

	if err != nil {
		return nil, fmt.Errorf("extract %s (%s): %w", path, format, err)
	}

	// Build raw text from sections.
	var sb strings.Builder
	for i, s := range sections {
		if i > 0 {
			sb.WriteByte('\n')
		}
		if s.Title != "" {
			sb.WriteString(s.Title)
			sb.WriteByte('\n')
		}
		sb.WriteString(s.Text)
	}

	return &Document{
		Path:     path,
		Format:   format,
		Title:    title,
		Sections: sections,
		RawText:  sb.String(),
		Quality:  pdfQuality,
	}, nil
}

// SupportedFormats returns all supported format extensions.
func SupportedFormats() []string {
	return []string{"docx", "odt", "pdf", "md", "txt", "html"}
}

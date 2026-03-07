// CLAUDE:SUMMARY Defines MarkdownConverter + KeyResolver callback types, and convertToMarkdown pipeline step (5.5).
// CLAUDE:DEPENDS sas_chunker, store
// CLAUDE:EXPORTS MarkdownConverter, KeyResolver
package sas_ingester

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hazyhaar/pkg/sas_chunker"
)

// extFromMIME returns a file extension for the given MIME type so that
// downstream format detectors (e.g. docpipe) can identify the format
// from the assembled temporary file path. Falls back to ".bin".
func extFromMIME(mime string) string {
	base := strings.SplitN(mime, ";", 2)[0]
	base = strings.TrimSpace(strings.ToLower(base))
	switch base {
	case "text/markdown":
		return ".md"
	case "text/plain":
		return ".txt"
	case "text/html":
		return ".html"
	case "application/pdf":
		return ".pdf"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/vnd.oasis.opendocument.text":
		return ".odt"
	default:
		return ".bin"
	}
}

// KeyResolver resolves a horoskey (API key delivered to LLMs) to the owner
// identity (JWT sub) for billing and authorization.
// Returns the ownerSub string, or error if the key is invalid/expired/revoked.
//
// This is a callback: sas_ingester defines the interface, the binary or the
// auth layer provides the implementation (e.g. lookup in horos_ID).
type KeyResolver func(ctx context.Context, horoskey string) (ownerSub string, err error)

// MarkdownConverter converts a file at the given path to markdown.
// The MIME type is provided for format hints.
// Returns markdown text (with frontmatter) or error.
//
// This is a callback: sas_ingester defines the interface, the binary
// provides the implementation (e.g. docpipe). This avoids a circular
// import since docpipe lives in chrc/ which imports hazyhaar_pkg.
type MarkdownConverter func(ctx context.Context, filePath string, mime string) (string, error)

// assembleChunks assembles chunk files into a single temporary file.
// The caller owns the returned file and must os.Remove it when done.
func (ing *Ingester) assembleChunks(dossierID, sha256, mime string) (string, error) {
	chunkDir := filepath.Join(ing.Config.ChunksDir, dossierID, sha256)
	tmpFile := filepath.Join(chunkDir, "assembled"+extFromMIME(mime))
	if err := sas_chunker.Assemble(chunkDir, tmpFile, nil); err != nil {
		return "", fmt.Errorf("assemble: %w", err)
	}
	return tmpFile, nil
}

// convertToMarkdown calls the MarkdownConverter callback on an already-assembled
// file and stores the result in the DB.
// Returns empty string with nil error if no converter is configured (skip).
func (ing *Ingester) convertToMarkdown(ctx context.Context, assembledPath, sha256, dossierID, mime string) (string, error) {
	if ing.MarkdownConverter == nil {
		return "", nil
	}

	md, err := ing.MarkdownConverter(ctx, assembledPath, mime)
	if err != nil {
		return "", fmt.Errorf("convert: %w", err)
	}

	if err := ing.Store.StoreMarkdown(sha256, dossierID, md); err != nil {
		return "", fmt.Errorf("store markdown: %w", err)
	}

	slog.Info("markdown conversion complete",
		"component", "ingester", "sha256", sha256, "dossier_id", dossierID,
		"markdown_len", len(md))

	return md, nil
}

// isClaimsCandidate returns true if the MIME type indicates a document
// that may need Claude Vision for text extraction (PDF scans, images).
func isClaimsCandidate(mime string) bool {
	mime = strings.ToLower(mime)
	switch {
	case mime == "application/pdf":
		return true
	case strings.HasPrefix(mime, "image/"):
		return true
	default:
		return false
	}
}

// convertToPNGPages converts a PDF or image file to PNG page images.
// For images (JPEG/PNG/TIFF), returns the path as-is (single "page"), empty tmpDir.
// For PDFs, shells out to pdftoppm (poppler-utils) to produce one PNG per page.
// Returns the list of PNG file paths and the tmpDir (caller must os.RemoveAll if non-empty).
func (ing *Ingester) convertToPNGPages(filePath, mime string) ([]string, string, error) {
	mime = strings.ToLower(mime)

	// Images are already single-page — return as-is.
	if strings.HasPrefix(mime, "image/") {
		return []string{filePath}, "", nil
	}

	// PDF → pdftoppm.
	if mime == "application/pdf" {
		return splitPDFToPages(filePath)
	}

	return nil, "", fmt.Errorf("unsupported MIME for claims conversion: %s", mime)
}

// splitPDFToPages converts a PDF to PNG pages via pdftoppm (poppler-utils).
// Returns paths to generated PNGs and the tmpDir. The caller must os.RemoveAll(tmpDir).
func splitPDFToPages(pdfPath string) ([]string, string, error) {
	tmpDir, err := os.MkdirTemp("", "sas-pdf2png-*")
	if err != nil {
		return nil, "", fmt.Errorf("create temp dir: %w", err)
	}

	outPrefix := filepath.Join(tmpDir, "page")

	cmd := exec.Command("pdftoppm", "-png", "-r", "200", pdfPath, outPrefix)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(tmpDir)
		return nil, "", fmt.Errorf("pdftoppm failed: %w: %s", err, out)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		os.RemoveAll(tmpDir)
		return nil, "", fmt.Errorf("read temp dir: %w", err)
	}

	var pages []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".png" {
			pages = append(pages, filepath.Join(tmpDir, e.Name()))
		}
	}

	if len(pages) == 0 {
		os.RemoveAll(tmpDir)
		return nil, "", fmt.Errorf("pdftoppm produced no pages for %s", pdfPath)
	}

	// Sort lexicographically — pdftoppm names are page-01.png, page-02.png, etc.
	sort.Strings(pages)
	return pages, tmpDir, nil
}

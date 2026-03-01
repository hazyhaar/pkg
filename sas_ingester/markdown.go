// CLAUDE:SUMMARY Defines MarkdownConverter + KeyResolver callback types, and convertToMarkdown pipeline step (5.5).
// CLAUDE:DEPENDS sas_chunker, store
// CLAUDE:EXPORTS MarkdownConverter, KeyResolver
package sas_ingester

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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

// convertToMarkdown assembles chunks into a temporary file, calls the
// MarkdownConverter callback, and stores the result in the DB.
// Returns empty string with nil error if no converter is configured (skip).
func (ing *Ingester) convertToMarkdown(ctx context.Context, sha256, dossierID, mime string) (string, error) {
	if ing.MarkdownConverter == nil {
		return "", nil
	}

	chunkDir := filepath.Join(ing.Config.ChunksDir, dossierID, sha256)

	// Assemble chunks into a temporary file whose extension matches the
	// original format so that docpipe can detect it from the path.
	tmpFile := filepath.Join(chunkDir, "assembled"+extFromMIME(mime))
	if err := sas_chunker.Assemble(chunkDir, tmpFile, nil); err != nil {
		return "", fmt.Errorf("assemble: %w", err)
	}
	defer os.Remove(tmpFile)

	md, err := ing.MarkdownConverter(ctx, tmpFile, mime)
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

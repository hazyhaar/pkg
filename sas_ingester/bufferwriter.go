// CLAUDE:SUMMARY Writes markdown files with YAML frontmatter into the HORAG buffer directory for vectorization.
// CLAUDE:DEPENDS docpipe (via MarkdownConverter callback)
// CLAUDE:EXPORTS BufferWriter, NewBufferWriter, WithBufferWriter
package sas_ingester

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// BufferWriter writes .md files with YAML frontmatter into the HORAG buffer
// directory. HORAG's watcher picks them up for chunking and embedding.
//
// Nil-safe: calling Write on a nil *BufferWriter is a no-op.
type BufferWriter struct {
	bufferDir string
}

// NewBufferWriter creates a BufferWriter that writes to the given directory.
// Returns nil if bufferDir is empty (feature disabled).
func NewBufferWriter(bufferDir string) *BufferWriter {
	if bufferDir == "" {
		return nil
	}
	return &BufferWriter{bufferDir: bufferDir}
}

// Write creates a .md file with YAML frontmatter in the buffer directory.
// Uses atomic write (tmp → rename) to prevent HORAG from reading partial files.
//
// Frontmatter fields match what HORAG watcher expects:
//   - id: sha256 hash (unique per document)
//   - dossier_id: tenant routing
//   - content_hash: dedup key ("sha256:<hash>")
//   - source_type: "document" (distinguishes from veille sources)
//   - title: original filename or extracted title
func (bw *BufferWriter) Write(_ context.Context, dossierID, sha256, title, markdown string) error {
	if bw == nil {
		return nil
	}
	if markdown == "" {
		return nil
	}

	// Build frontmatter + body.
	content := fmt.Sprintf(`---
id: %q
dossier_id: %q
content_hash: "sha256:%s"
source_type: "document"
title: %q
---
%s
`, sha256, dossierID, sha256, title, markdown)

	// Atomic write: .md.tmp → os.Rename → .md
	finalPath := filepath.Join(bw.bufferDir, sha256+".md")
	tmpPath := finalPath + ".tmp"

	if err := os.MkdirAll(bw.bufferDir, 0755); err != nil {
		return fmt.Errorf("mkdir buffer: %w", err)
	}

	if err := os.WriteFile(tmpPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename to final: %w", err)
	}

	slog.Info("buffer writer: wrote markdown",
		"component", "bufferwriter",
		"dossier_id", dossierID,
		"sha256", sha256,
		"size", len(content))

	return nil
}

// WithBufferWriter sets the BufferWriter for writing markdown to the HORAG buffer.
// The writer is called after step 5.5 (markdown conversion) for ready pieces.
// If nil (default), the buffer step is silently skipped.
func WithBufferWriter(bw *BufferWriter) IngesterOption {
	return func(ing *Ingester) { ing.BufferWriter = bw }
}

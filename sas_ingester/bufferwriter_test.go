package sas_ingester

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBufferWriter_Write(t *testing.T) {
	// WHAT: BufferWriter creates .md file with correct frontmatter.
	// WHY: HORAG watcher depends on specific frontmatter fields.
	dir := t.TempDir()
	bw := NewBufferWriter(dir)

	err := bw.Write(context.Background(), "dos_abc123", "sha256hash", "My Document", "# Hello\n\nWorld")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	path := filepath.Join(dir, "sha256hash.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	content := string(data)

	// Check frontmatter fields.
	if !strings.Contains(content, `id: "sha256hash"`) {
		t.Error("missing id in frontmatter")
	}
	if !strings.Contains(content, `dossier_id: "dos_abc123"`) {
		t.Error("missing dossier_id in frontmatter")
	}
	if !strings.Contains(content, `content_hash: "sha256:sha256hash"`) {
		t.Error("missing content_hash in frontmatter")
	}
	if !strings.Contains(content, `source_type: "document"`) {
		t.Error("missing source_type in frontmatter")
	}
	if !strings.Contains(content, `title: "My Document"`) {
		t.Error("missing title in frontmatter")
	}

	// Check body.
	if !strings.Contains(content, "# Hello") {
		t.Error("missing markdown body")
	}
}

func TestBufferWriter_AtomicWrite(t *testing.T) {
	// WHAT: No .tmp file remains after successful write.
	// WHY: Atomic write ensures HORAG never reads partial files.
	dir := t.TempDir()
	bw := NewBufferWriter(dir)

	_ = bw.Write(context.Background(), "dos_1", "abc", "title", "content")

	// .tmp must not exist.
	tmpPath := filepath.Join(dir, "abc.md.tmp")
	if _, err := os.Stat(tmpPath); err == nil {
		t.Error(".tmp file should not exist after successful write")
	}

	// .md must exist.
	mdPath := filepath.Join(dir, "abc.md")
	if _, err := os.Stat(mdPath); err != nil {
		t.Error(".md file should exist after write")
	}
}

func TestBufferWriter_NilSafe(t *testing.T) {
	// WHAT: Calling Write on nil BufferWriter is a no-op.
	// WHY: Nil-safety pattern used throughout sas_ingester (like MarkdownConverter).
	var bw *BufferWriter
	err := bw.Write(context.Background(), "dos_1", "sha", "title", "content")
	if err != nil {
		t.Fatalf("nil BufferWriter.Write should return nil, got: %v", err)
	}
}

func TestBufferWriter_EmptyMarkdown(t *testing.T) {
	// WHAT: Empty markdown is a no-op (no file created).
	// WHY: Some documents fail extraction, producing empty markdown.
	dir := t.TempDir()
	bw := NewBufferWriter(dir)

	err := bw.Write(context.Background(), "dos_1", "sha", "title", "")
	if err != nil {
		t.Fatalf("empty markdown should return nil, got: %v", err)
	}

	path := filepath.Join(dir, "sha.md")
	if _, err := os.Stat(path); err == nil {
		t.Error("no file should be created for empty markdown")
	}
}

func TestNewBufferWriter_EmptyDir(t *testing.T) {
	// WHAT: NewBufferWriter("") returns nil (feature disabled).
	// WHY: Empty buffer_dir in config = disabled.
	bw := NewBufferWriter("")
	if bw != nil {
		t.Error("expected nil for empty dir")
	}
}

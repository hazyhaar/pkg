// CLAUDE:SUMMARY One-shot CLI to reprocess ready pieces through docpipe extraction and HORAG buffer writing.
// CLAUDE:DEPENDS docpipe, sas_chunker, sas_ingester
// CLAUDE:EXPORTS none

// reprocess — One-shot script to reprocess existing ready pieces through
// docpipe + BufferWriter. Run once after deploying the new sas_ingester
// with MarkdownConverter + BufferWriter support.
//
// Usage:
//   reprocess -db /path/to/sas_ingester.db -chunks /path/to/chunks -buffer /path/to/buffer/pending
//
// What it does:
//   1. Lists all pieces with state=ready that have no markdown yet
//   2. For each: assemble chunks → docpipe.Extract → store markdown + write .md to buffer
//   3. Skips pieces that already have markdown (idempotent)
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/hazyhaar/pkg/docpipe"
	"github.com/hazyhaar/pkg/sas_chunker"
	"github.com/hazyhaar/pkg/sas_ingester"

	_ "modernc.org/sqlite"
)

func main() {
	dbPath := flag.String("db", "sas_ingester.db", "Path to sas_ingester.db")
	chunksDir := flag.String("chunks", "chunks", "Path to chunks directory")
	bufferDir := flag.String("buffer", "", "Path to HORAG buffer/pending/ directory")
	dryRun := flag.Bool("dry-run", false, "List pieces without processing")
	flag.Parse()

	if *bufferDir == "" {
		slog.Error("buffer dir required (-buffer)")
		os.Exit(1)
	}

	// Open DB read-write (need to write markdown).
	db, err := sql.Open("sqlite", *dbPath+"?_txlock=immediate&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)")
	if err != nil {
		slog.Error("open db", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// List ready pieces without markdown.
	rows, err := db.Query(`
		SELECT p.sha256, p.dossier_id, p.mime
		FROM pieces p
		LEFT JOIN pieces_markdown pm ON p.sha256 = pm.sha256 AND p.dossier_id = pm.dossier_id
		WHERE p.state = 'ready' AND pm.sha256 IS NULL
		ORDER BY p.created_at
	`)
	if err != nil {
		db.Close()
		slog.Error("query pieces", "error", err)
		os.Exit(1)
	}
	defer rows.Close()

	type piece struct {
		sha256    string
		dossierID string
		mime      string
	}
	var pieces []piece
	for rows.Next() {
		var p piece
		if err := rows.Scan(&p.sha256, &p.dossierID, &p.mime); err != nil {
			slog.Error("scan", "error", err)
			continue
		}
		pieces = append(pieces, p)
	}
	if err := rows.Err(); err != nil {
		slog.Error("rows iteration", "error", err)
		os.Exit(1)
	}
	rows.Close()

	slog.Info("pieces to reprocess", "count", len(pieces))

	if *dryRun {
		for _, p := range pieces {
			fmt.Printf("  %s  dossier=%s  mime=%s\n", p.sha256, p.dossierID, p.mime)
		}
		return
	}

	// MIME → extension mapping for docpipe (needs correct extension to detect format).
	mimeExt := map[string]string{
		"application/pdf":                            ".pdf",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": ".docx",
		"application/vnd.oasis.opendocument.text":    ".odt",
		"application/msword":                         ".doc",
		"text/plain":                                 ".txt",
		"text/markdown":                              ".md",
		"text/html":                                  ".html",
		"application/zip":                            ".docx", // most ZIPs in SAS are DOCX
	}

	// Setup pipeline components.
	pipe := docpipe.New(docpipe.Config{})
	bw := sas_ingester.NewBufferWriter(*bufferDir)
	ctx := context.Background()

	var ok, skipped, errCount int

	for i, p := range pieces {
		chunkDir := filepath.Join(*chunksDir, p.dossierID, p.sha256)

		// Check chunks exist.
		if _, err := os.Stat(chunkDir); err != nil {
			slog.Warn("chunks dir missing, skipping", "sha256", p.sha256, "dir", chunkDir)
			skipped++
			continue
		}

		// Resolve file extension from MIME type (strip params like "; charset=utf-8").
		baseMime := p.mime
		if idx := strings.Index(baseMime, ";"); idx != -1 {
			baseMime = strings.TrimSpace(baseMime[:idx])
		}
		ext, supported := mimeExt[baseMime]
		if !supported {
			slog.Warn("unsupported mime, skipping", "sha256", p.sha256, "mime", p.mime)
			skipped++
			continue
		}

		// Assemble chunks into temp file with correct extension.
		tmpFile := filepath.Join(chunkDir, "assembled.reprocess"+ext)
		if err := sas_chunker.Assemble(chunkDir, tmpFile, nil); err != nil {
			slog.Error("assemble failed", "sha256", p.sha256, "error", err)
			errCount++
			continue
		}

		// Extract markdown via docpipe.
		doc, err := pipe.Extract(ctx, tmpFile)
		os.Remove(tmpFile)
		if err != nil {
			slog.Warn("docpipe extract failed", "sha256", p.sha256, "mime", p.mime, "error", err)
			errCount++
			continue
		}

		md := doc.RawText
		if md == "" {
			slog.Warn("empty extraction", "sha256", p.sha256)
			skipped++
			continue
		}

		// Store markdown in DB.
		if _, err := db.Exec(
			`INSERT OR REPLACE INTO pieces_markdown (sha256, dossier_id, markdown, created_at) VALUES (?, ?, ?, datetime('now'))`,
			p.sha256, p.dossierID, md,
		); err != nil {
			slog.Error("store markdown", "sha256", p.sha256, "error", err)
			errCount++
			continue
		}

		// Write .md to HORAG buffer.
		title := doc.Title
		if title == "" {
			title = p.sha256
		}
		if err := bw.Write(ctx, p.dossierID, p.sha256, title, md); err != nil {
			slog.Error("buffer write", "sha256", p.sha256, "error", err)
			errCount++
			continue
		}

		ok++
		slog.Info("reprocessed",
			"progress", fmt.Sprintf("%d/%d", i+1, len(pieces)),
			"sha256", p.sha256,
			"dossier_id", p.dossierID,
			"md_len", len(md),
			"title", title)
	}

	slog.Info("reprocess complete", "ok", ok, "skipped", skipped, "errors", errCount, "total", len(pieces))
}

package sas_ingester

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hazyhaar/pkg/sas_chunker"
)

// UploadResult holds the result of receiving a file upload.
type UploadResult struct {
	SHA256       string `json:"sha256"`
	SizeBytes    int64  `json:"size_bytes"`
	ChunkCount   int    `json:"chunk_count"`
	Deduplicated bool   `json:"deduplicated"`
}

// ReceiveFile reads from r, streams to a temp file while hashing, checks
// dedup, then delegates chunking to sas_chunker.Split so that manifest.json,
// Verify and Assemble all work out of the box.
func ReceiveFile(r io.Reader, dossierID string, cfg *Config, store *Store) (*UploadResult, error) {
	// Stage 1: stream to a temp file while hashing.
	tmpFile, err := os.CreateTemp(cfg.ChunksDir, "upload-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	hasher := sha256.New()
	limited := io.LimitReader(r, cfg.MaxFileBytes()+1) // +1 to detect overflow
	written, err := io.Copy(tmpFile, io.TeeReader(limited, hasher))
	tmpFile.Close()
	if err != nil {
		return nil, fmt.Errorf("receive upload: %w", err)
	}
	if written > cfg.MaxFileBytes() {
		return nil, fmt.Errorf("file exceeds max size (%d MB)", cfg.MaxFileMB)
	}

	hash := hex.EncodeToString(hasher.Sum(nil))

	// Stage 2: check dedup.
	existing, err := store.GetPiece(hash, dossierID)
	if err != nil {
		return nil, fmt.Errorf("dedup check: %w", err)
	}
	if existing != nil {
		return &UploadResult{
			SHA256:       hash,
			SizeBytes:    written,
			Deduplicated: true,
		}, nil
	}

	// Stage 3: delegate chunking to sas_chunker.Split.
	// This produces chunk_00000.bin ... chunk_NNNNN.bin + manifest.json,
	// making sas_chunker.Verify() and sas_chunker.Assemble() compatible.
	chunkDir := filepath.Join(cfg.ChunksDir, dossierID, hash)
	manifest, err := sas_chunker.Split(tmpPath, chunkDir, cfg.ChunkSizeBytes(), nil)
	if err != nil {
		return nil, fmt.Errorf("chunk file: %w", err)
	}

	// Record each chunk in the DB for tracking.
	for _, cm := range manifest.Chunks {
		if err := store.InsertChunk(hash, dossierID, cm.Index, cm.SHA256, true); err != nil {
			return nil, fmt.Errorf("record chunk %d: %w", cm.Index, err)
		}
	}

	// Stage 4: record the piece.
	now := time.Now().UTC().Format(time.RFC3339)
	if err := store.InsertPiece(&Piece{
		SHA256:        hash,
		DossierID:     dossierID,
		State:         "received",
		SizeBytes:     written,
		InjectionRisk: "none",
		ClamAVStatus:  "pending",
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		return nil, fmt.Errorf("insert piece: %w", err)
	}

	return &UploadResult{
		SHA256:     hash,
		SizeBytes:  written,
		ChunkCount: manifest.TotalChunks,
	}, nil
}

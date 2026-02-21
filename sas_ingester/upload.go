package sas_ingester

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hazyhaar/pkg/horosafe"
	"github.com/hazyhaar/pkg/sas_chunker"
)

// UploadResult holds the result of receiving a file upload.
type UploadResult struct {
	SHA256       string `json:"sha256"`
	SizeBytes    int64  `json:"size_bytes"`
	ChunkCount   int    `json:"chunk_count"`
	State        string `json:"state,omitempty"`
	Deduplicated bool   `json:"deduplicated"`
}

// ReceiveFile reads from r, streams directly to chunks via sas_chunker.SplitReader
// (no intermediate temp file), checks dedup, and records the piece in the DB.
// Manifest.json, Verify() and Assemble() all work out of the box.
func ReceiveFile(r io.Reader, dossierID string, cfg *Config, store *Store) (*UploadResult, error) {
	// Path traversal guard: dossierID is used in file paths.
	if err := horosafe.ValidateIdentifier(dossierID); err != nil {
		return nil, fmt.Errorf("invalid dossier ID: %w", err)
	}

	// Stage 1: stream directly into chunks while hashing.
	// We use a counting reader to enforce the max file size limit.
	limited := io.LimitReader(r, cfg.MaxFileBytes()+1) // +1 to detect overflow

	// Use a unique suffix to prevent race conditions when two uploads
	// for the same dossier happen concurrently.
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return nil, fmt.Errorf("generate upload suffix: %w", err)
	}
	chunkDir := filepath.Join(cfg.ChunksDir, dossierID, "incoming-"+hex.EncodeToString(suffix[:]))
	manifest, err := sas_chunker.SplitReader(limited, "upload", chunkDir, cfg.ChunkSizeBytes(), nil)
	if err != nil {
		os.RemoveAll(chunkDir)
		return nil, fmt.Errorf("chunk stream: %w", err)
	}

	if manifest.OriginalSize > cfg.MaxFileBytes() {
		os.RemoveAll(chunkDir)
		return nil, fmt.Errorf("file exceeds max size (%d MB)", cfg.MaxFileMB)
	}

	hash := manifest.OriginalSHA256

	// Stage 2: check dedup.
	existing, err := store.GetPiece(hash, dossierID)
	if err != nil {
		os.RemoveAll(chunkDir)
		return nil, fmt.Errorf("dedup check: %w", err)
	}
	if existing != nil {
		os.RemoveAll(chunkDir)
		return &UploadResult{
			SHA256:       hash,
			SizeBytes:    manifest.OriginalSize,
			Deduplicated: true,
		}, nil
	}

	// Stage 3: rename the incoming dir to its final location keyed by hash.
	finalDir := filepath.Join(cfg.ChunksDir, dossierID, hash)
	if err := os.MkdirAll(filepath.Dir(finalDir), 0755); err != nil {
		return nil, fmt.Errorf("prepare dir: %w", err)
	}
	if err := os.Rename(chunkDir, finalDir); err != nil {
		// Rename can fail across filesystems; fall back to keeping incoming dir.
		finalDir = chunkDir
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
		SizeBytes:     manifest.OriginalSize,
		InjectionRisk: "none",
		ClamAVStatus:  "pending",
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		return nil, fmt.Errorf("insert piece: %w", err)
	}

	return &UploadResult{
		SHA256:     hash,
		SizeBytes:  manifest.OriginalSize,
		ChunkCount: manifest.TotalChunks,
	}, nil
}

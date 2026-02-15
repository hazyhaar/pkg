package sas_ingester

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// UploadResult holds the result of receiving a file upload.
type UploadResult struct {
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"size_bytes"`
	ChunkCount  int    `json:"chunk_count"`
	Deduplicated bool  `json:"deduplicated"`
}

// ReceiveFile reads from r, streams to disk in chunks, computes SHA256, and returns the result.
// If the file already exists (same sha256 + dossierID), it returns Deduplicated=true.
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

	// Stage 3: chunk the temp file.
	chunkSize := cfg.ChunkSizeBytes()
	chunkDir := filepath.Join(cfg.ChunksDir, dossierID, hash)
	if err := os.MkdirAll(chunkDir, 0755); err != nil {
		return nil, fmt.Errorf("create chunk dir: %w", err)
	}

	src, err := os.Open(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("reopen temp: %w", err)
	}
	defer src.Close()

	buf := make([]byte, chunkSize)
	var chunkIdx int
	for {
		n, err := io.ReadFull(src, buf)
		if n == 0 {
			break
		}
		data := buf[:n]

		chunkHasher := sha256.New()
		chunkHasher.Write(data)
		chunkHash := hex.EncodeToString(chunkHasher.Sum(nil))

		chunkPath := filepath.Join(chunkDir, fmt.Sprintf("chunk_%05d.bin", chunkIdx))
		if err2 := os.WriteFile(chunkPath, data, 0644); err2 != nil {
			return nil, fmt.Errorf("write chunk %d: %w", chunkIdx, err2)
		}

		if err2 := store.InsertChunk(hash, dossierID, chunkIdx, chunkHash, true); err2 != nil {
			return nil, fmt.Errorf("record chunk %d: %w", chunkIdx, err2)
		}

		chunkIdx++

		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read chunk %d: %w", chunkIdx, err)
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
		ChunkCount: chunkIdx,
	}, nil
}

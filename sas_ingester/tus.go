package sas_ingester

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/hazyhaar/pkg/horosafe"
	"github.com/hazyhaar/pkg/sas_chunker"
)

// TusHandler manages tus resumable upload operations.
type TusHandler struct {
	store *Store
	cfg   *Config
	newID func() string
}

// NewTusHandler creates a new tus handler.
func NewTusHandler(store *Store, cfg *Config, newID func() string) *TusHandler {
	return &TusHandler{store: store, cfg: cfg, newID: newID}
}

// Create initialises a new tus upload. Returns the upload ID.
func (h *TusHandler) Create(dossierID, ownerSub string, totalSize int64) (*TusUpload, error) {
	// Path traversal guard: dossierID is used in file paths.
	if err := horosafe.ValidateIdentifier(dossierID); err != nil {
		return nil, fmt.Errorf("invalid dossier ID: %w", err)
	}
	if totalSize <= 0 {
		return nil, fmt.Errorf("Upload-Length must be > 0")
	}
	if totalSize > h.cfg.MaxFileBytes() {
		return nil, fmt.Errorf("file exceeds max size (%d MB)", h.cfg.MaxFileMB)
	}

	uploadID := h.newID()
	chunkDir := filepath.Join(h.cfg.ChunksDir, dossierID, "_tus_"+uploadID)
	if err := os.MkdirAll(chunkDir, 0755); err != nil {
		return nil, fmt.Errorf("create tus dir: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	u := &TusUpload{
		UploadID:    uploadID,
		DossierID:   dossierID,
		OwnerJWTSub: ownerSub,
		TotalSize:   totalSize,
		OffsetBytes: 0,
		ChunkDir:    chunkDir,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := h.store.CreateTusUpload(u); err != nil {
		return nil, fmt.Errorf("create tus record: %w", err)
	}

	return u, nil
}

// GetOffset returns the current offset for a tus upload.
func (h *TusHandler) GetOffset(uploadID string) (*TusUpload, error) {
	u, err := h.store.GetTusUpload(uploadID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, fmt.Errorf("upload not found: %s", uploadID)
	}
	return u, nil
}

// Patch appends data to an existing tus upload.
// The caller must pass the Upload-Offset header value for consistency check.
// Returns the new offset after appending.
func (h *TusHandler) Patch(uploadID string, clientOffset int64, body io.Reader) (int64, error) {
	u, err := h.store.GetTusUpload(uploadID)
	if err != nil {
		return 0, err
	}
	if u == nil {
		return 0, fmt.Errorf("upload not found: %s", uploadID)
	}
	if u.Completed {
		return 0, fmt.Errorf("upload already completed")
	}
	if clientOffset != u.OffsetBytes {
		return 0, fmt.Errorf("offset mismatch: client=%d server=%d", clientOffset, u.OffsetBytes)
	}

	// Append to the partial file.
	partialPath := filepath.Join(u.ChunkDir, "partial.bin")
	f, err := os.OpenFile(partialPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return 0, fmt.Errorf("open partial: %w", err)
	}

	remaining := u.TotalSize - u.OffsetBytes
	limited := io.LimitReader(body, remaining)
	written, err := io.Copy(f, limited)
	f.Close()
	if err != nil {
		return 0, fmt.Errorf("write partial: %w", err)
	}

	newOffset := u.OffsetBytes + written
	if err := h.store.UpdateTusOffset(uploadID, newOffset); err != nil {
		return 0, fmt.Errorf("update offset: %w", err)
	}

	return newOffset, nil
}

// Complete finalises a tus upload: hashes the partial file, chunks it via
// SplitReader, and returns the result ready for the ingestion pipeline.
// The caller should then proceed with Ingest-like steps (scan, metadata, etc).
func (h *TusHandler) Complete(uploadID string) (*UploadResult, error) {
	u, err := h.store.GetTusUpload(uploadID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, fmt.Errorf("upload not found: %s", uploadID)
	}
	if u.Completed {
		return nil, fmt.Errorf("upload already completed")
	}
	if u.OffsetBytes != u.TotalSize {
		return nil, fmt.Errorf("upload incomplete: %d/%d bytes", u.OffsetBytes, u.TotalSize)
	}

	partialPath := filepath.Join(u.ChunkDir, "partial.bin")

	// Hash the complete file.
	f, err := os.Open(partialPath)
	if err != nil {
		return nil, fmt.Errorf("open partial for hash: %w", err)
	}
	hasher := sha256.New()
	size, err := io.Copy(hasher, f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("hash partial: %w", err)
	}
	hash := hex.EncodeToString(hasher.Sum(nil))

	// Check dedup.
	existing, err := h.store.GetPiece(hash, u.DossierID)
	if err != nil {
		return nil, fmt.Errorf("dedup check: %w", err)
	}
	if existing != nil {
		// Clean up tus state.
		os.RemoveAll(u.ChunkDir)
		h.store.CompleteTusUpload(uploadID)
		return &UploadResult{
			SHA256:       hash,
			SizeBytes:    size,
			Deduplicated: true,
		}, nil
	}

	// Chunk via SplitReader from the partial file.
	finalDir := filepath.Join(h.cfg.ChunksDir, u.DossierID, hash)
	pf, err := os.Open(partialPath)
	if err != nil {
		return nil, fmt.Errorf("open partial for split: %w", err)
	}
	manifest, err := sas_chunker.SplitReader(pf, "upload", finalDir, h.cfg.ChunkSizeBytes(), nil)
	pf.Close()
	if err != nil {
		return nil, fmt.Errorf("chunk tus upload: %w", err)
	}

	// Record chunks in DB.
	for _, cm := range manifest.Chunks {
		if err := h.store.InsertChunk(hash, u.DossierID, cm.Index, cm.SHA256, true); err != nil {
			return nil, fmt.Errorf("record chunk %d: %w", cm.Index, err)
		}
	}

	// Record piece.
	now := time.Now().UTC().Format(time.RFC3339)
	if err := h.store.InsertPiece(&Piece{
		SHA256:        hash,
		DossierID:     u.DossierID,
		State:         "received",
		SizeBytes:     size,
		InjectionRisk: "none",
		ClamAVStatus:  "pending",
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		return nil, fmt.Errorf("insert piece: %w", err)
	}

	// Clean up: remove the tus staging directory (chunks are in finalDir).
	os.RemoveAll(u.ChunkDir)
	h.store.CompleteTusUpload(uploadID)

	return &UploadResult{
		SHA256:     hash,
		SizeBytes:  size,
		ChunkCount: manifest.TotalChunks,
	}, nil
}

// ParseUploadLength parses the Upload-Length header value.
func ParseUploadLength(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("missing Upload-Length header")
	}
	return strconv.ParseInt(s, 10, 64)
}

// ParseUploadOffset parses the Upload-Offset header value.
func ParseUploadOffset(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("missing Upload-Offset header")
	}
	return strconv.ParseInt(s, 10, 64)
}

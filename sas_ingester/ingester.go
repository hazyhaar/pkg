package sas_ingester

import (
	"fmt"
	"io"
	"log"
	"path/filepath"
)

// Ingester is the main pipeline orchestrator.
type Ingester struct {
	Store  *Store
	Config *Config
	Router *Router
}

// NewIngester creates a fully wired ingester.
func NewIngester(cfg *Config) (*Ingester, error) {
	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	router := NewRouter(store, cfg)
	return &Ingester{
		Store:  store,
		Config: cfg,
		Router: router,
	}, nil
}

// Close releases resources.
func (ing *Ingester) Close() error {
	return ing.Store.Close()
}

// Ingest runs the full pipeline for a single upload:
//  1. Receive file → chunk + hash + dedup
//  2. Extract metadata
//  3. Security scan (ClamAV, zip bomb, polyglot, macro)
//  4. Prompt injection scan
//  5. Update piece state
//  6. Enqueue webhook routes
func (ing *Ingester) Ingest(r io.Reader, dossierID, ownerSub string) (*IngestResult, error) {
	// Ensure dossier exists.
	if err := ing.Store.EnsureDossier(dossierID, ownerSub); err != nil {
		return nil, fmt.Errorf("ensure dossier: %w", err)
	}

	// Step 1: receive & chunk.
	upload, err := ReceiveFile(r, dossierID, ing.Config, ing.Store)
	if err != nil {
		return nil, fmt.Errorf("receive: %w", err)
	}

	result := &IngestResult{
		SHA256:       upload.SHA256,
		SizeBytes:    upload.SizeBytes,
		DossierID:    dossierID,
		Deduplicated: upload.Deduplicated,
	}

	if upload.Deduplicated {
		result.State = "deduplicated"
		return result, nil
	}

	chunkDir := filepath.Join(ing.Config.ChunksDir, dossierID, upload.SHA256)

	// Step 2: metadata extraction on the first chunk (approximation for the whole file).
	firstChunk := filepath.Join(chunkDir, "chunk_00000.bin")
	meta, err := ExtractMetadata(firstChunk)
	if err != nil {
		log.Printf("[ingester] metadata warning: %v", err)
	}

	// Step 3: security scan on the first chunk.
	scanResult, err := ScanFile(firstChunk, ing.Config)
	if err != nil {
		log.Printf("[ingester] scan warning: %v", err)
		scanResult = &ScanResult{ClamAV: "error"}
	}
	result.Scan = scanResult

	if scanResult.Blocked {
		if err := ing.Store.UpdatePieceState(upload.SHA256, dossierID, "blocked"); err != nil {
			log.Printf("[ingester] update state: %v", err)
		}
		result.State = "blocked"
		return result, nil
	}

	// Step 4: prompt injection scan across all chunks.
	injResult := ScanChunksInjection(chunkDir, upload.ChunkCount)
	result.Injection = injResult

	// Step 5: update piece metadata & state.
	clamStatus := scanResult.ClamAV
	finalState := "ready"
	if injResult.Risk == "high" {
		finalState = "flagged"
	}

	var mime, metadataJSON string
	if meta != nil {
		mime = meta.MIME
		metadataJSON = MetadataJSON(meta)
	}

	if err := ing.Store.UpdatePieceMetadata(
		upload.SHA256, dossierID,
		mime, metadataJSON, injResult.Risk, clamStatus, finalState,
	); err != nil {
		return nil, fmt.Errorf("update metadata: %w", err)
	}

	result.State = finalState
	result.MIME = mime

	// Step 6: enqueue routes.
	piece, _ := ing.Store.GetPiece(upload.SHA256, dossierID)
	if piece != nil && finalState == "ready" {
		if err := ing.Router.EnqueueRoutes(piece); err != nil {
			log.Printf("[ingester] enqueue routes: %v", err)
		}
	}

	return result, nil
}

// IngestResult is the result of a full ingestion pipeline run.
type IngestResult struct {
	SHA256       string           `json:"sha256"`
	SizeBytes    int64            `json:"size_bytes"`
	DossierID    string           `json:"dossier_id"`
	State        string           `json:"state"`
	MIME         string           `json:"mime,omitempty"`
	Deduplicated bool             `json:"deduplicated,omitempty"`
	Scan         *ScanResult      `json:"scan,omitempty"`
	Injection    *InjectionResult `json:"injection,omitempty"`
}

package sas_ingester

import (
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"time"

	"github.com/hazyhaar/pkg/idgen"
	"github.com/hazyhaar/pkg/observability"
)

// Ingester is the main pipeline orchestrator.
type Ingester struct {
	Store   *Store
	Config  *Config
	Router  *Router
	Audit   *observability.AuditLogger
	Metrics *observability.MetricsManager
	Events  *observability.EventLogger
	NewID   idgen.Generator
}

// IngesterOption configures an Ingester.
type IngesterOption func(*Ingester)

// WithAudit sets the audit logger.
func WithAudit(a *observability.AuditLogger) IngesterOption {
	return func(ing *Ingester) { ing.Audit = a }
}

// WithMetrics sets the metrics manager.
func WithMetrics(m *observability.MetricsManager) IngesterOption {
	return func(ing *Ingester) { ing.Metrics = m }
}

// WithEvents sets the event logger.
func WithEvents(e *observability.EventLogger) IngesterOption {
	return func(ing *Ingester) { ing.Events = e }
}

// WithIDGenerator sets the ID generator for dossier IDs.
func WithIDGenerator(g idgen.Generator) IngesterOption {
	return func(ing *Ingester) { ing.NewID = g }
}

// NewIngester creates a fully wired ingester.
func NewIngester(cfg *Config, opts ...IngesterOption) (*Ingester, error) {
	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	router := NewRouter(store, cfg)
	ing := &Ingester{
		Store:  store,
		Config: cfg,
		Router: router,
		NewID:  idgen.Prefixed("dos_", idgen.Default),
	}
	for _, o := range opts {
		o(ing)
	}
	return ing, nil
}

// Close releases resources.
func (ing *Ingester) Close() error {
	if ing.Audit != nil {
		ing.Audit.Close()
	}
	if ing.Metrics != nil {
		ing.Metrics.Close()
	}
	return ing.Store.Close()
}

func (ing *Ingester) auditLog(operation, userID, params, result string, err error, duration time.Duration) {
	if ing.Audit == nil {
		return
	}
	entry := ing.Audit.NewAuditEntry("sas_ingester", operation, params, result, err, duration)
	entry.UserID = userID
	ing.Audit.LogAsync(entry)
}

func (ing *Ingester) recordEvent(eventType, entityID, userID, action, details string, success bool) {
	if ing.Events == nil {
		return
	}
	ing.Events.LogEvent(context.Background(), observability.BusinessEvent{
		EventType:   eventType,
		ServiceName: "sas_ingester",
		EntityType:  "piece",
		EntityID:    entityID,
		UserID:      userID,
		Action:      action,
		Details:     details,
		Success:     success,
	})
}

func (ing *Ingester) recordMetric(name string, value float64, unit string) {
	if ing.Metrics == nil {
		return
	}
	ing.Metrics.RecordSimple(name, value, unit)
}

// Ingest runs the full pipeline for a single upload:
//  1. Receive file → chunk + hash + dedup
//  2. Extract metadata
//  3. Security scan (ClamAV, zip bomb, polyglot, macro)
//  4. Prompt injection scan
//  5. Update piece state
//  6. Enqueue webhook routes
func (ing *Ingester) Ingest(r io.Reader, dossierID, ownerSub string) (*IngestResult, error) {
	start := time.Now()

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
		ing.auditLog("upload.dedup", ownerSub, dossierID, upload.SHA256, nil, time.Since(start))
		ing.recordEvent("upload", upload.SHA256, ownerSub, "dedup", "", true)
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
	scanStart := time.Now()
	scanResult, err := ScanFile(firstChunk, ing.Config)
	if err != nil {
		log.Printf("[ingester] scan warning: %v", err)
		scanResult = &ScanResult{ClamAV: "error"}
	}
	ing.recordMetric("scan_duration_ms", float64(time.Since(scanStart).Milliseconds()), "milliseconds")
	result.Scan = scanResult

	if scanResult.Blocked {
		if err := ing.Store.UpdatePieceState(upload.SHA256, dossierID, "blocked"); err != nil {
			log.Printf("[ingester] update state: %v", err)
		}
		result.State = "blocked"
		ing.auditLog("upload.blocked", ownerSub, dossierID, upload.SHA256, nil, time.Since(start))
		ing.recordEvent("upload", upload.SHA256, ownerSub, "blocked",
			fmt.Sprintf(`{"warnings":%q}`, scanResult.Warnings), false)
		ing.recordMetric(observability.MetricTaskProcessedCount, 1, "count")
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

	duration := time.Since(start)
	ing.auditLog("upload.complete", ownerSub, dossierID,
		fmt.Sprintf("sha256=%s state=%s", upload.SHA256, finalState), nil, duration)
	ing.recordEvent("upload", upload.SHA256, ownerSub, "complete",
		fmt.Sprintf(`{"state":%q,"mime":%q}`, finalState, mime), true)
	ing.recordMetric(observability.MetricWorkflowDurationMs, float64(duration.Milliseconds()), "milliseconds")
	ing.recordMetric(observability.MetricTaskProcessedCount, 1, "count")

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

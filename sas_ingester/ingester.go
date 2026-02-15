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

// RecoverStalePieces finds pieces stuck in intermediate states (received,
// scanned) from a previous crash and marks them for re-processing.
// Call this once at boot before accepting new uploads.
func (ing *Ingester) RecoverStalePieces() {
	for _, state := range []string{"received", "scanned"} {
		pieces, err := ing.Store.ListPiecesByState(state)
		if err != nil {
			log.Printf("[recovery] list %s pieces: %v", state, err)
			continue
		}
		for _, p := range pieces {
			log.Printf("[recovery] re-queuing stale piece sha256=%s dossier=%s state=%s", p.SHA256, p.DossierID, p.State)
			// Mark as received so the pipeline can retry from the appropriate step.
			if err := ing.Store.UpdatePieceState(p.SHA256, p.DossierID, "received"); err != nil {
				log.Printf("[recovery] update piece: %v", err)
			}
		}
		if len(pieces) > 0 {
			log.Printf("[recovery] re-queued %d pieces from state %q", len(pieces), state)
		}
	}
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
//
// Identity cutoff: ownerSub is used only for EnsureDossier and the pre-cutoff
// audit log. After that, it is erased and never passed downstream.
func (ing *Ingester) Ingest(r io.Reader, dossierID, ownerSub string) (*IngestResult, error) {
	return ing.IngestWithToken(r, dossierID, ownerSub, "")
}

// IngestWithToken is like Ingest but also captures the original JWT token for
// jwt_passthru routes. The token is cleared after enqueuing routes.
func (ing *Ingester) IngestWithToken(r io.Reader, dossierID, ownerSub, originalToken string) (*IngestResult, error) {
	start := time.Now()

	// --- Pre-cutoff phase: ownerSub is available ---
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
		// Pre-cutoff audit: ownerSub is explicitly labeled as Sas-internal.
		ing.auditLog("sas.upload.dedup", ownerSub, dossierID, upload.SHA256, nil, time.Since(start))
		return result, nil
	}

	// Pre-cutoff audit: last use of ownerSub before erasure.
	ing.auditLog("sas.upload.received", ownerSub, dossierID, upload.SHA256, nil, time.Since(start))

	// --- Identity cutoff: ownerSub is erased from this point ---
	// The pipeline state carries only the opaque dossier_id.
	// originalToken is passed for jwt_passthru routes only.
	return ing.processPipeline(upload, dossierID, originalToken, result, start)
}

// IngestFromUpload runs pipeline steps 2-6 for an upload that was already
// received (e.g. via tus resumable upload). The UploadResult must have SHA256,
// SizeBytes, and ChunkCount set; the piece must already exist in the DB.
func (ing *Ingester) IngestFromUpload(upload *UploadResult, dossierID, ownerSub string) (*IngestResult, error) {
	return ing.IngestFromUploadWithToken(upload, dossierID, ownerSub, "")
}

// IngestFromUploadWithToken is like IngestFromUpload but captures the original
// JWT token for jwt_passthru routes.
func (ing *Ingester) IngestFromUploadWithToken(upload *UploadResult, dossierID, ownerSub, originalToken string) (*IngestResult, error) {
	start := time.Now()

	// --- Pre-cutoff phase ---
	if err := ing.Store.EnsureDossier(dossierID, ownerSub); err != nil {
		return nil, fmt.Errorf("ensure dossier: %w", err)
	}

	// Pre-cutoff audit.
	ing.auditLog("sas.tus.received", ownerSub, dossierID, upload.SHA256, nil, time.Since(start))

	// --- Identity cutoff ---
	result := &IngestResult{
		SHA256:    upload.SHA256,
		SizeBytes: upload.SizeBytes,
		DossierID: dossierID,
	}

	return ing.processPipeline(upload, dossierID, originalToken, result, start)
}

// processPipeline runs steps 2-6: metadata, scan, injection, state, routes.
// INVARIANT: ownerSub is NOT passed here. Only the opaque dossierID is used.
// The originalToken is forwarded to jwt_passthru routes only and never logged.
func (ing *Ingester) processPipeline(upload *UploadResult, dossierID, originalToken string, result *IngestResult, start time.Time) (*IngestResult, error) {
	chunkDir := filepath.Join(ing.Config.ChunksDir, dossierID, upload.SHA256)

	// Step 2: metadata extraction across all chunks (header + trailer + entropy).
	meta, err := ExtractFullMetadata(chunkDir, upload.ChunkCount)
	if err != nil {
		log.Printf("[ingester] metadata warning: %v", err)
	}

	// Step 3: security scan on first + last chunks (+ ClamAV on all).
	scanStart := time.Now()
	scanResult, err := ScanChunks(chunkDir, upload.ChunkCount, ing.Config)
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
		// Post-cutoff: no ownerSub in logs, only opaque dossierID.
		ing.auditLog("sas.pipeline.blocked", "", dossierID, upload.SHA256, nil, time.Since(start))
		ing.recordEvent("upload", upload.SHA256, "", "blocked",
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
	// Post-cutoff audit: no ownerSub, only opaque dossierID.
	ing.auditLog("sas.pipeline.complete", "", dossierID,
		fmt.Sprintf("sha256=%s state=%s", upload.SHA256, finalState), nil, duration)
	ing.recordEvent("upload", upload.SHA256, "", "complete",
		fmt.Sprintf(`{"state":%q,"mime":%q}`, finalState, mime), true)
	ing.recordMetric(observability.MetricWorkflowDurationMs, float64(duration.Milliseconds()), "milliseconds")
	ing.recordMetric(observability.MetricTaskProcessedCount, 1, "count")

	// Step 6: enqueue routes (originalToken forwarded for jwt_passthru).
	piece, _ := ing.Store.GetPiece(upload.SHA256, dossierID)
	if piece != nil && finalState == "ready" {
		if err := ing.Router.EnqueueRoutesWithToken(piece, originalToken); err != nil {
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

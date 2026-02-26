// CLAUDE:SUMMARY Registers sas_ingester service handlers on a connectivity.Router.
// CLAUDE:DEPENDS connectivity, ingester, store
// CLAUDE:EXPORTS RegisterConnectivity
package sas_ingester

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hazyhaar/pkg/connectivity"
)

// MaxBase64Bytes is the maximum decoded file size accepted via base64 upload (10 MB).
// For larger files, use TUS resumable upload over HTTP.
const MaxBase64Bytes = 10 * 1024 * 1024

// RegisterConnectivity registers sas_ingester service handlers on a connectivity Router.
//
// All handlers require authentication: either owner_sub (pre-authenticated
// service-to-service) or horoskey (API key, resolved via KeyResolver).
//
// Registered services:
//
//	sas_create_context  — create a dossier for piece ingestion (requires auth)
//	sas_upload_piece    — upload a file as base64 (≤10 MB), run full pipeline
//	sas_query_piece     — get piece metadata + markdown availability
//	sas_list_pieces     — list pieces in a dossier (optional state filter)
//	sas_get_markdown    — retrieve markdown text for a piece
//	sas_retry_routes    — retry failed webhook deliveries for a piece
func RegisterConnectivity(router *connectivity.Router, ing *Ingester) {
	router.RegisterLocal("sas_create_context", handleCreateContext(ing))
	router.RegisterLocal("sas_upload_piece", handleUploadPiece(ing))
	router.RegisterLocal("sas_query_piece", handleQueryPiece(ing))
	router.RegisterLocal("sas_list_pieces", handleListPieces(ing))
	router.RegisterLocal("sas_get_markdown", handleGetMarkdown(ing))
	router.RegisterLocal("sas_retry_routes", handleRetryRoutes(ing))
}

// authFields is embedded in every request to carry authentication.
type authFields struct {
	OwnerSub string `json:"owner_sub"`
	Horoskey string `json:"horoskey"`
}

// --- sas_create_context ---

func handleCreateContext(ing *Ingester) connectivity.Handler {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		var req struct {
			authFields
			Name string `json:"name"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}

		ownerSub, err := ing.resolveOwner(ctx, req.OwnerSub, req.Horoskey)
		if err != nil {
			return nil, err
		}

		dossierID := ing.NewID()
		if req.Name == "" {
			req.Name = "contexte jetable"
		}

		now := time.Now().UTC().Format(time.RFC3339)
		if err := ing.Store.CreateDossier(&Dossier{
			ID:          dossierID,
			OwnerJWTSub: ownerSub,
			Name:        req.Name,
			CreatedAt:   now,
		}); err != nil {
			return nil, fmt.Errorf("create dossier: %w", err)
		}

		return json.Marshal(map[string]any{
			"dossier_id": dossierID,
			"name":       req.Name,
			"created_at": now,
			"howto": "Dossier créé. Uploadez des pièces via sas_upload_piece (base64, ≤10 Mo) " +
				"ou via TUS HTTP pour les gros fichiers. Récupérez le markdown via sas_get_markdown.",
		})
	}
}

// --- sas_upload_piece ---

func handleUploadPiece(ing *Ingester) connectivity.Handler {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		var req struct {
			authFields
			DossierID     string `json:"dossier_id"`
			Filename      string `json:"filename"`
			ContentBase64 string `json:"content_base64"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}

		ownerSub, err := ing.resolveOwner(ctx, req.OwnerSub, req.Horoskey)
		if err != nil {
			return nil, err
		}

		if req.DossierID == "" || req.ContentBase64 == "" {
			return nil, fmt.Errorf("dossier_id and content_base64 are required")
		}

		data, err := base64.StdEncoding.DecodeString(req.ContentBase64)
		if err != nil {
			return nil, fmt.Errorf("base64 decode: %w", err)
		}

		if len(data) > MaxBase64Bytes {
			return json.Marshal(map[string]any{
				"error": fmt.Sprintf("file too large: %d bytes (max %d bytes = 10 MB via base64)", len(data), MaxBase64Bytes),
				"howto": "Pour les fichiers > 10 Mo, utilisez le protocole TUS (resumable upload) " +
					"sur l'endpoint HTTP du serveur SAS. Exemple : " +
					"POST /uploads avec les headers Tus-Resumable, Upload-Length, Upload-Metadata. " +
					"Le TUS supporte jusqu'à " + fmt.Sprintf("%d", ing.Config.MaxFileMB) + " Mo et reprend automatiquement en cas de coupure.",
			})
		}

		result, err := ing.Ingest(bytes.NewReader(data), req.DossierID, ownerSub)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
}

// --- sas_query_piece ---

func handleQueryPiece(ing *Ingester) connectivity.Handler {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		var req struct {
			authFields
			DossierID string `json:"dossier_id"`
			SHA256    string `json:"sha256"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}

		if _, err := ing.resolveOwner(ctx, req.OwnerSub, req.Horoskey); err != nil {
			return nil, err
		}

		piece, err := ing.Store.GetPiece(req.SHA256, req.DossierID)
		if err != nil {
			return nil, err
		}
		if piece == nil {
			return nil, fmt.Errorf("piece not found: %s/%s", req.DossierID, req.SHA256)
		}

		hasMd, _ := ing.Store.HasMarkdown(req.SHA256, req.DossierID)

		return json.Marshal(map[string]any{
			"piece":        piece,
			"has_markdown": hasMd,
		})
	}
}

// --- sas_list_pieces ---

func handleListPieces(ing *Ingester) connectivity.Handler {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		var req struct {
			authFields
			DossierID string `json:"dossier_id"`
			State     string `json:"state"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}

		if _, err := ing.resolveOwner(ctx, req.OwnerSub, req.Horoskey); err != nil {
			return nil, err
		}

		if req.DossierID == "" {
			return nil, fmt.Errorf("dossier_id is required")
		}

		var pieces []*Piece
		var err error
		if req.State != "" {
			all, e := ing.Store.ListPieces(req.DossierID)
			if e != nil {
				return nil, e
			}
			for _, p := range all {
				if p.State == req.State {
					pieces = append(pieces, p)
				}
			}
		} else {
			pieces, err = ing.Store.ListPieces(req.DossierID)
		}
		if err != nil {
			return nil, err
		}

		return json.Marshal(map[string]any{
			"pieces": pieces,
			"count":  len(pieces),
		})
	}
}

// --- sas_get_markdown ---

func handleGetMarkdown(ing *Ingester) connectivity.Handler {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		var req struct {
			authFields
			DossierID string `json:"dossier_id"`
			SHA256    string `json:"sha256"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}

		if _, err := ing.resolveOwner(ctx, req.OwnerSub, req.Horoskey); err != nil {
			return nil, err
		}

		md, err := ing.Store.GetMarkdown(req.SHA256, req.DossierID)
		if err != nil {
			return nil, err
		}
		if md == "" {
			return nil, fmt.Errorf("no markdown found for %s/%s", req.DossierID, req.SHA256)
		}

		return json.Marshal(map[string]any{
			"markdown":   md,
			"sha256":     req.SHA256,
			"dossier_id": req.DossierID,
		})
	}
}

// --- sas_retry_routes ---

func handleRetryRoutes(ing *Ingester) connectivity.Handler {
	return func(ctx context.Context, payload []byte) ([]byte, error) {
		var req struct {
			authFields
			DossierID string `json:"dossier_id"`
			SHA256    string `json:"sha256"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, fmt.Errorf("decode: %w", err)
		}

		if _, err := ing.resolveOwner(ctx, req.OwnerSub, req.Horoskey); err != nil {
			return nil, err
		}

		routes, err := ing.Store.ListRoutes(req.SHA256, req.DossierID)
		if err != nil {
			return nil, err
		}

		retried := 0
		now := time.Now().UTC().Format(time.RFC3339)
		for _, r := range routes {
			if r.Attempts > 0 {
				if err := ing.Store.UpdateRouteAttempt(r.PieceSHA256, r.DossierID, r.Target, 0, "", now); err != nil {
					continue
				}
				retried++
			}
		}

		return json.Marshal(map[string]any{
			"retried": retried,
		})
	}
}

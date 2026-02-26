// CLAUDE:SUMMARY Registers sas_ingester MCP tools via kit.RegisterMCPTool — 6 tools for LLM access.
// CLAUDE:DEPENDS kit, mcp, ingester, store
// CLAUDE:EXPORTS RegisterMCP
package sas_ingester

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hazyhaar/pkg/kit"
)

// RegisterMCP registers sas_ingester tools on an MCP server.
//
// All tools require a horoskey (API key) for authentication.
// The key is resolved via Ingester.KeyResolver to identify the owner
// and authorize the operation.
//
// Tools: sas_create_context, sas_upload_piece, sas_query_piece,
// sas_list_pieces, sas_get_markdown, sas_retry_routes.
func RegisterMCP(srv *mcp.Server, ing *Ingester) {
	registerCreateContextTool(srv, ing)
	registerUploadPieceTool(srv, ing)
	registerQueryPieceTool(srv, ing)
	registerListPiecesTool(srv, ing)
	registerGetMarkdownTool(srv, ing)
	registerRetryRoutesTool(srv, ing)
}

func mcpSchema(properties map[string]any, required []string) map[string]any {
	s := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// horoskey property reused across all tools.
var horoskeyProp = map[string]any{
	"type":        "string",
	"description": "API key (hk_xxx) for authentication and billing",
}

// --- sas_create_context ---

type createContextReq struct {
	Horoskey string `json:"horoskey"`
	Name     string `json:"name"`
}

func registerCreateContextTool(srv *mcp.Server, ing *Ingester) {
	tool := &mcp.Tool{
		Name: "sas_create_context",
		Description: "Create a dossier (context) for uploading and processing files. " +
			"Requires a horoskey for authentication. Returns a dossier_id to use with other sas_* tools. " +
			"Upload files via sas_upload_piece (base64, ≤10 MB) or via TUS HTTP for larger files.",
		InputSchema: mcpSchema(map[string]any{
			"horoskey": horoskeyProp,
			"name":     map[string]any{"type": "string", "description": "Optional human-readable name for the context"},
		}, []string{"horoskey"}),
	}

	endpoint := func(ctx context.Context, req any) (any, error) {
		r := req.(*createContextReq)
		ownerSub, err := ing.resolveOwner(ctx, "", r.Horoskey)
		if err != nil {
			return nil, err
		}

		dossierID := ing.NewID()
		name := r.Name
		if name == "" {
			name = "contexte jetable"
		}
		now := time.Now().UTC().Format(time.RFC3339)
		if err := ing.Store.CreateDossier(&Dossier{
			ID:          dossierID,
			OwnerJWTSub: ownerSub,
			Name:        name,
			CreatedAt:   now,
		}); err != nil {
			return nil, fmt.Errorf("create dossier: %w", err)
		}
		return map[string]any{
			"dossier_id": dossierID,
			"name":       name,
			"created_at": now,
			"howto": "Dossier créé. Uploadez des pièces via sas_upload_piece (base64, ≤10 Mo) " +
				"ou via TUS HTTP pour les gros fichiers. Récupérez le markdown via sas_get_markdown.",
		}, nil
	}

	decode := func(req *mcp.CallToolRequest) (*kit.MCPDecodeResult, error) {
		var r createContextReq
		if err := json.Unmarshal(req.Params.Arguments, &r); err != nil {
			return nil, err
		}
		return &kit.MCPDecodeResult{Request: &r}, nil
	}

	kit.RegisterMCPTool(srv, tool, endpoint, decode)
}

// --- sas_upload_piece ---

type uploadPieceReq struct {
	Horoskey      string `json:"horoskey"`
	DossierID     string `json:"dossier_id"`
	Filename      string `json:"filename"`
	ContentBase64 string `json:"content_base64"`
}

func registerUploadPieceTool(srv *mcp.Server, ing *Ingester) {
	tool := &mcp.Tool{
		Name: "sas_upload_piece",
		Description: "Upload a file as base64, run the full ingestion pipeline (chunking, security scan, " +
			"dedup, markdown conversion), and return the result. Maximum 10 MB via base64. " +
			"For larger files, use TUS resumable upload over HTTP.",
		InputSchema: mcpSchema(map[string]any{
			"horoskey":       horoskeyProp,
			"dossier_id":     map[string]any{"type": "string", "description": "Target dossier ID (from sas_create_context)"},
			"filename":       map[string]any{"type": "string", "description": "Original filename (for MIME detection)"},
			"content_base64": map[string]any{"type": "string", "description": "File content encoded as base64"},
		}, []string{"horoskey", "dossier_id", "content_base64"}),
	}

	endpoint := func(ctx context.Context, req any) (any, error) {
		r := req.(*uploadPieceReq)

		ownerSub, err := ing.resolveOwner(ctx, "", r.Horoskey)
		if err != nil {
			return nil, err
		}

		data, err := base64.StdEncoding.DecodeString(r.ContentBase64)
		if err != nil {
			return nil, fmt.Errorf("base64 decode: %w", err)
		}

		if len(data) > MaxBase64Bytes {
			return map[string]any{
				"error": fmt.Sprintf("file too large: %d bytes (max %d bytes = 10 MB via base64)", len(data), MaxBase64Bytes),
				"howto": "Pour les fichiers > 10 Mo, utilisez le protocole TUS (resumable upload) " +
					"sur l'endpoint HTTP du serveur SAS. Envoyez un POST /uploads avec les headers " +
					"Tus-Resumable: 1.0.0, Upload-Length: <taille>, Upload-Metadata: dossier_id <base64>, filename <base64>. " +
					fmt.Sprintf("Le TUS supporte jusqu'à %d Mo et reprend automatiquement en cas de coupure réseau.", ing.Config.MaxFileMB),
			}, nil
		}

		result, err := ing.Ingest(bytes.NewReader(data), r.DossierID, ownerSub)
		if err != nil {
			return nil, err
		}
		return result, nil
	}

	decode := func(req *mcp.CallToolRequest) (*kit.MCPDecodeResult, error) {
		var r uploadPieceReq
		if err := json.Unmarshal(req.Params.Arguments, &r); err != nil {
			return nil, err
		}
		return &kit.MCPDecodeResult{Request: &r}, nil
	}

	kit.RegisterMCPTool(srv, tool, endpoint, decode)
}

// --- sas_query_piece ---

type queryPieceReq struct {
	Horoskey  string `json:"horoskey"`
	DossierID string `json:"dossier_id"`
	SHA256    string `json:"sha256"`
}

func registerQueryPieceTool(srv *mcp.Server, ing *Ingester) {
	tool := &mcp.Tool{
		Name:        "sas_query_piece",
		Description: "Get piece metadata, state, scan results, and markdown availability for a specific piece.",
		InputSchema: mcpSchema(map[string]any{
			"horoskey":   horoskeyProp,
			"dossier_id": map[string]any{"type": "string", "description": "Dossier ID"},
			"sha256":     map[string]any{"type": "string", "description": "SHA-256 hash of the piece"},
		}, []string{"horoskey", "dossier_id", "sha256"}),
	}

	endpoint := func(ctx context.Context, req any) (any, error) {
		r := req.(*queryPieceReq)
		if _, err := ing.resolveOwner(ctx, "", r.Horoskey); err != nil {
			return nil, err
		}
		piece, err := ing.Store.GetPiece(r.SHA256, r.DossierID)
		if err != nil {
			return nil, err
		}
		if piece == nil {
			return nil, fmt.Errorf("piece not found: %s/%s", r.DossierID, r.SHA256)
		}
		hasMd, _ := ing.Store.HasMarkdown(r.SHA256, r.DossierID)
		return map[string]any{
			"piece":        piece,
			"has_markdown": hasMd,
		}, nil
	}

	decode := func(req *mcp.CallToolRequest) (*kit.MCPDecodeResult, error) {
		var r queryPieceReq
		if err := json.Unmarshal(req.Params.Arguments, &r); err != nil {
			return nil, err
		}
		return &kit.MCPDecodeResult{Request: &r}, nil
	}

	kit.RegisterMCPTool(srv, tool, endpoint, decode)
}

// --- sas_list_pieces ---

type listPiecesReq struct {
	Horoskey  string `json:"horoskey"`
	DossierID string `json:"dossier_id"`
	State     string `json:"state"`
}

func registerListPiecesTool(srv *mcp.Server, ing *Ingester) {
	tool := &mcp.Tool{
		Name:        "sas_list_pieces",
		Description: "List all pieces in a dossier, with optional state filter (received, scanned, ready, flagged, blocked).",
		InputSchema: mcpSchema(map[string]any{
			"horoskey":   horoskeyProp,
			"dossier_id": map[string]any{"type": "string", "description": "Dossier ID"},
			"state":      map[string]any{"type": "string", "description": "Optional state filter"},
		}, []string{"horoskey", "dossier_id"}),
	}

	endpoint := func(ctx context.Context, req any) (any, error) {
		r := req.(*listPiecesReq)
		if _, err := ing.resolveOwner(ctx, "", r.Horoskey); err != nil {
			return nil, err
		}
		all, err := ing.Store.ListPieces(r.DossierID)
		if err != nil {
			return nil, err
		}
		var pieces []*Piece
		if r.State != "" {
			for _, p := range all {
				if p.State == r.State {
					pieces = append(pieces, p)
				}
			}
		} else {
			pieces = all
		}
		return map[string]any{
			"pieces": pieces,
			"count":  len(pieces),
		}, nil
	}

	decode := func(req *mcp.CallToolRequest) (*kit.MCPDecodeResult, error) {
		var r listPiecesReq
		if err := json.Unmarshal(req.Params.Arguments, &r); err != nil {
			return nil, err
		}
		return &kit.MCPDecodeResult{Request: &r}, nil
	}

	kit.RegisterMCPTool(srv, tool, endpoint, decode)
}

// --- sas_get_markdown ---

type getMarkdownReq struct {
	Horoskey  string `json:"horoskey"`
	DossierID string `json:"dossier_id"`
	SHA256    string `json:"sha256"`
}

func registerGetMarkdownTool(srv *mcp.Server, ing *Ingester) {
	tool := &mcp.Tool{
		Name:        "sas_get_markdown",
		Description: "Retrieve the markdown text extracted from a piece. Returns the full markdown content.",
		InputSchema: mcpSchema(map[string]any{
			"horoskey":   horoskeyProp,
			"dossier_id": map[string]any{"type": "string", "description": "Dossier ID"},
			"sha256":     map[string]any{"type": "string", "description": "SHA-256 hash of the piece"},
		}, []string{"horoskey", "dossier_id", "sha256"}),
	}

	endpoint := func(ctx context.Context, req any) (any, error) {
		r := req.(*getMarkdownReq)
		if _, err := ing.resolveOwner(ctx, "", r.Horoskey); err != nil {
			return nil, err
		}
		md, err := ing.Store.GetMarkdown(r.SHA256, r.DossierID)
		if err != nil {
			return nil, err
		}
		if md == "" {
			return nil, fmt.Errorf("no markdown found for %s/%s", r.DossierID, r.SHA256)
		}
		return map[string]any{
			"markdown":   md,
			"sha256":     r.SHA256,
			"dossier_id": r.DossierID,
		}, nil
	}

	decode := func(req *mcp.CallToolRequest) (*kit.MCPDecodeResult, error) {
		var r getMarkdownReq
		if err := json.Unmarshal(req.Params.Arguments, &r); err != nil {
			return nil, err
		}
		return &kit.MCPDecodeResult{Request: &r}, nil
	}

	kit.RegisterMCPTool(srv, tool, endpoint, decode)
}

// --- sas_retry_routes ---

type retryRoutesReq struct {
	Horoskey  string `json:"horoskey"`
	DossierID string `json:"dossier_id"`
	SHA256    string `json:"sha256"`
}

func registerRetryRoutesTool(srv *mcp.Server, ing *Ingester) {
	tool := &mcp.Tool{
		Name:        "sas_retry_routes",
		Description: "Retry failed webhook deliveries for a piece. Resets attempt counters on all routes.",
		InputSchema: mcpSchema(map[string]any{
			"horoskey":   horoskeyProp,
			"dossier_id": map[string]any{"type": "string", "description": "Dossier ID"},
			"sha256":     map[string]any{"type": "string", "description": "SHA-256 hash of the piece"},
		}, []string{"horoskey", "dossier_id", "sha256"}),
	}

	endpoint := func(ctx context.Context, req any) (any, error) {
		r := req.(*retryRoutesReq)
		if _, err := ing.resolveOwner(ctx, "", r.Horoskey); err != nil {
			return nil, err
		}
		routes, err := ing.Store.ListRoutes(r.SHA256, r.DossierID)
		if err != nil {
			return nil, err
		}
		retried := 0
		now := time.Now().UTC().Format(time.RFC3339)
		for _, route := range routes {
			if route.Attempts > 0 {
				if err := ing.Store.UpdateRouteAttempt(route.PieceSHA256, route.DossierID, route.Target, 0, "", now); err != nil {
					continue
				}
				retried++
			}
		}
		return map[string]any{
			"retried": retried,
		}, nil
	}

	decode := func(req *mcp.CallToolRequest) (*kit.MCPDecodeResult, error) {
		var r retryRoutesReq
		if err := json.Unmarshal(req.Params.Arguments, &r); err != nil {
			return nil, err
		}
		return &kit.MCPDecodeResult{Request: &r}, nil
	}

	kit.RegisterMCPTool(srv, tool, endpoint, decode)
}
